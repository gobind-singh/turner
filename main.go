package main

import (
	"crypto/tls"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"

	"gortc.io/stun"
	"gortc.io/turn"
	"gortc.io/turnc"
)

var (
	server = flag.String("server",
		fmt.Sprintf("localhost:3478"),
		"turn server address",
	)

	username = flag.String("u", "user", "username")
	password = flag.String("p", "secret", "password")
)

type TurnConnection struct {
	//	ControlClient     *turnc.Client
	//	DataClient        *turnc.Client
	ControlConnection *turnc.Connection
	DataConnection    net.Conn //*turnc.Connection
	ControlConn       net.Conn
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func bufHeader(src http.Header) []byte {
	buf := make([]byte, 0)
	for k, vv := range src {
		buf = append(buf, []byte(k)...)
		buf = append(buf, []byte(":")...)
		for _, v := range vv {
			buf = append(buf, []byte(v)...)
		}
		buf = append(buf, []byte("\r\n")...)
	}
	return buf
}

// this function is such an ugly hack but I'm tired and it works
// look at replacing with real code that does io.Copy and
// better buffer handling
// this drains http headers, constructs manual method line
// and manual host line
// then sends everything to the server
func handleHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Println(r.Method)

	target := r.URL.Host
	if target == "" {
		w.Write([]byte("This is a HTTP Proxy, use it as such"))
		return
	}

	port := r.URL.Port()

	if port == "" {
		port = "80"
	}
	peer := target
	if strings.Index(target, ":") == -1 {
		peer = fmt.Sprintf("%s:%s", target, port)
	}
	fmt.Println(peer)

	turnConnection, err := connectTurn(peer)
	if err != nil || turnConnection.DataConnection == nil {
		if turnConnection.DataConnection != nil {
			turnConnection.DataConnection.Close()
		}
		if turnConnection.ControlConnection != nil {
			turnConnection.ControlConnection.Close()
		}

		http.Error(w, fmt.Sprintf("Proxy encountered error: %s", err), http.StatusInternalServerError)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "webserver doesn't support hijacking", http.StatusInternalServerError)
		return
	}
	conn, bufwr, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Don't forget to close the connection:
	defer conn.Close()
	defer turnConnection.ControlConn.Close()
	//ugly hack to recreate same function that could be achieved with httputil.DumpRequest
	// create method line
	methodLine := fmt.Sprintf("%s %s %s\r\n", r.Method, r.URL.Path, r.Proto)
	hostLine := fmt.Sprintf("Host: %s\r\n", target)
	turnConnection.DataConnection.Write([]byte(methodLine))
	turnConnection.DataConnection.Write([]byte(hostLine))
	turnConnection.DataConnection.Write(bufHeader(r.Header))
	turnConnection.DataConnection.Write([]byte("\r\n"))
	//drain body

	io.Copy(turnConnection.DataConnection, r.Body)

	/*
		// Would have loved to just use DumpRequest here
		// but this drops the Host header as go follows rfc7230
		// https://github.com/golang/go/issues/16265
		// which ends up giving problems as receiving servers return 400 "missing host header"
		dump, err := httputil.DumpRequest(r, true)
		if err != nil {
			http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			return
		}
		destConn.Write(dump)
		fmt.Printf("%q\n", dump)
	*/
	//defer destConn.Close()

	//turnConnection.DataConnection.SetReadBuffer(2048)

	//timeoutDuration := 5 * time.Second
	//bufReader := bufio.NewReader(destConn)

	defer turnConnection.ControlConnection.Close()
	defer turnConnection.DataConnection.Close()

	buf := make([]byte, 1024)
	for {
		// Set a deadline for reading. Read operation will fail if no data
		// is received after deadline.
		//turnConnection.DataConnection.SetReadDeadline(time.Now().Add(timeoutDuration))

		n, err := turnConnection.DataConnection.Read(buf)
		if err != nil {
			break
		}
		/*
			// Read tokens delimited by newline
			bytes, err := destConn.Read()
			if err != nil {
				fmt.Println(err)
				break
			}
		*/
		//fmt.Printf("%s", bytes)
		bufwr.Write(buf[:n])
		bufwr.Flush()
	}

}

//func transfer(destination io.WriteCloser, source io.ReadCloser, wg sync.WaitGroup) {
func transfer(destination io.WriteCloser, source io.ReadCloser) {
	defer destination.Close()
	defer source.Close()
	//defer wg.Done()
	io.Copy(destination, source)
}

func handleProxyTun(w http.ResponseWriter, r *http.Request) {
	fmt.Println("CONNECT")

	target := r.URL.Host
	if target == "" {
		w.Write([]byte("This is a HTTP Proxy, use it as such"))
		return
	}

	port := r.URL.Port()

	if port == "" {
		port = "443"
	}
	peer := target
	if strings.Index(target, ":") == -1 {
		peer = fmt.Sprintf("%s:%s", target, port)
	}

	w.WriteHeader(http.StatusOK)
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	}

	turnConnection, err := connectTurn(peer)
	if err != nil || turnConnection.DataConnection == nil {
		if turnConnection.DataConnection != nil {
			turnConnection.DataConnection.Close()
		}
		if turnConnection.ControlConnection != nil {
			turnConnection.ControlConnection.Close()
		}

		http.Error(w, fmt.Sprintf("Proxy encountered error: %s", err), http.StatusInternalServerError)
		return
	}

	/*
		wg := sync.WaitGroup{}
		wg.Add(2)

		go transfer(turnConnection.DataConnection, clientConn, wg)
		go transfer(clientConn, turnConnection.DataConnection, wg)

		wg.Wait()
		fmt.Println("close!")
		turnConnection.ControlConnection.Close()
		turnConnection.DataConnection.Close()
	*/
	defer turnConnection.ControlConn.Close()
	defer turnConnection.DataConnection.Close()

	go transfer(turnConnection.DataConnection, clientConn)
	transfer(clientConn, turnConnection.DataConnection)
}

func connectTurn(target string) (TurnConnection, error) {
	turnConnection := TurnConnection{}
	var err error
	// Resolving to TURN server.
	raddr, err := net.ResolveTCPAddr("tcp", *server)
	if err != nil {
		fmt.Println(err)
		return turnConnection, err
	}
	c, err := net.DialTCP("tcp", nil, raddr)
	if err != nil {
		fmt.Println(err)
		return turnConnection, err
	}
	turnConnection.ControlConn = c
	fmt.Printf("dial server %s -> %s\n", c.LocalAddr(), c.RemoteAddr())
	controlClient, err := turnc.New(turnc.Options{
		Conn:     c,
		Username: *username,
		Password: *password,
	})

	if err != nil {
		fmt.Println(err)
		return turnConnection, err
	}
	a, err := controlClient.AllocateTCP()
	if err != nil {
		fmt.Println(err)
		return turnConnection, err
	}
	peerAddr, err := net.ResolveTCPAddr("tcp", target)
	if err != nil {
		fmt.Println(err)
		return turnConnection, err
	}
	fmt.Println("create peer")
	permission, err := a.Create(peerAddr.IP)
	if err != nil {
		fmt.Println(err)
		return turnConnection, err
	}
	fmt.Println("create peer permission")
	turnConnection.ControlConnection, err = permission.CreateTCP(peerAddr)
	if err != nil {
		fmt.Println(err)
		return turnConnection, err
	}

	fmt.Println("send connect request")
	var connid stun.RawAttribute
	if connid, err = turnConnection.ControlConnection.Connect(); err != nil {
		fmt.Println(err)
		return turnConnection, err
	}

	// setup bind
	fmt.Println("setting up bind")
	cd, err := net.DialTCP("tcp", nil, raddr)
	if err != nil {
		fmt.Println(err)
		return turnConnection, err
	}

	turnConnection.DataConnection = cd
	fmt.Println("bind client create")
	dataClient, err := turnc.New(turnc.Options{
		Conn:     cd,
		Username: *username,
		Password: *password,
	})
	if err != nil {
		fmt.Println(err)
		return turnConnection, err
	}

	fmt.Println("binding")
	err = dataClient.ConnectionBind(turn.ConnectionID(binary.BigEndian.Uint32(connid.Value)))
	if err != nil {
		fmt.Println(err)
		return turnConnection, err
	}

	return turnConnection, nil
}

func main() {
	flag.Parse()

	server := &http.Server{
		Addr: ":8080",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				handleProxyTun(w, r)
			} else {
				handleHTTP(w, r)
			}
		}),
		// Disable HTTP/2.
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
	}

	log.Fatal(server.ListenAndServe())

}
