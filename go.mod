module github.com/staaldraad/turner

require (
	github.com/staaldraad/turner/go-socks5 v0.0.0
	gortc.io/stun v1.22.1
	gortc.io/turn v0.11.2
	gortc.io/turnc v0.2.0
)

replace github.com/staaldraad/turner/go-socks5 => ./go-socks5

replace gortc.io/turnc => ../turnc

replace gortc.io/turn => ../turn

replace gortc.io/stun => ../../../gortc.io/stun
