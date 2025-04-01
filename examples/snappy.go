// Program snappy is a demonstration command line utility to drive a
// Snapmaker 2.0 A350.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"

	"zappem.net/pub/net/snappy"
)

var (
	config = flag.String("config", "snapmaker.config", "config file")
	home   = flag.Bool("home", false, "home device (required after power on)")
)

type Config struct {
	Token   string
	Address string
}

func main() {
	flag.Parse()

	data, err := os.ReadFile(*config)
	if err != nil {
		log.Fatalf("failed to read --config=%q: %v", *config, err)
	}
	var conf Config
	if err := json.Unmarshal(data, &conf); err != nil {
		log.Fatalf("failed to import %q: %v", *config, err)
	}

	c, err := snappy.NewConn(conf.Address, conf.Token)
	if err != nil {
		log.Fatalf("unable to connect to %q: %v", conf.Address, err)
	}
	defer c.Close()

	if err := c.Status(); err != nil {
		log.Fatalf("failed to read status: %v", err)
	}

	if !c.Homed() {
		if !*home {
			log.Fatal("device is not homed yet, use --home")
		}
	}

	ctx := context.Background()

	if *home {
		if err := c.Home(ctx); err != nil {
			log.Fatalf("failed to home device: %v", err)
		}
	}
}
