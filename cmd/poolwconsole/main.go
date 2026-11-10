// poolwconsole is the optional web console for poolwarden. It shells out to
// the poolwarden CLI for every read and mutation and embeds the SPA.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	"github.com/smarsh/poolwarden/internal/console"
)

func main() {
	var (
		addr  = flag.String("addr", envOr("PWCONSOLE_ADDR", "127.0.0.1:8080"), "listen address")
		token = flag.String("token", os.Getenv("PWCONSOLE_TOKEN"), "access token (required unless -token-file)")
		tfile = flag.String("token-file", os.Getenv("PWCONSOLE_TOKEN_FILE"), "file containing the access token")
		cli   = flag.String("cli", envOr("PWCONSOLE_CLI", "poolwarden"), "path to the poolwarden binary")
		cfg   = flag.String("config", envOr("POOLWARDEN_CONFIG", "/etc/poolwarden.yaml"), "poolwarden config file")
		state = flag.String("state", envOr("PWCONSOLE_STATE", "/var/lib/poolwarden"), "state dir for op journal + candidates")
		cert  = flag.String("tls-cert", os.Getenv("PWCONSOLE_TLS_CERT"), "TLS certificate (optional)")
		key   = flag.String("tls-key", os.Getenv("PWCONSOLE_TLS_KEY"), "TLS key (optional)")
	)
	flag.Parse()

	tok := *token
	if tok == "" && *tfile != "" {
		if b, err := os.ReadFile(*tfile); err == nil {
			tok = string(bytes.TrimSpace(b))
		}
	}
	if tok == "" {
		fmt.Fprintln(os.Stderr, "poolwconsole: --token or --token-file required")
		os.Exit(2)
	}

	var assets fs.FS
	if sub, err := fs.Sub(console.EmbeddedAssets, "dist"); err == nil {
		assets = sub
	}

	srv := console.NewServer(console.Options{
		Addr: *addr, Token: tok, CLIBin: *cli, Config: *cfg,
		StateDir: *state, Assets: assets,
		TLSCert: *cert, TLSKey: *key,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(os.Stderr, "poolwconsole listening on %s (cli=%s config=%s)\n", *addr, *cli, *cfg)
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "poolwconsole:", err)
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
