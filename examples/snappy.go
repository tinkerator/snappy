// Program snappy is a demonstration command line utility to drive a
// Snapmaker 2.0 A350.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"

	"zappem.net/pub/net/snappy"
)

var (
	config = flag.String("config", "snapmaker.config", "config file")
	home   = flag.Bool("home", false, "home device (required after power on)")
	photo  = flag.Bool("photo", false, "request a series of photos taken in a circle")
	zoom   = flag.Bool("zoom", false, "request a series of zoomed photos")
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

	if *photo {
		for i := 0; i < 9; i++ {
			theta := float64(i) / 9.0 * 2.0 * math.Pi
			r := 15.0
			log.Printf("taking photo %d (at %.2f deg)", i, theta/math.Pi*180)
			d, err := c.SnapJPEG(ctx, i, 192.5+r*math.Cos(theta), 170+r*math.Sin(theta), 170)
			if err != nil {
				log.Fatalf("photo grab failed: %v", err)
			}
			if err := os.WriteFile(fmt.Sprintf("photo%d.jpg", i), d, 0777); err != nil {
				log.Fatalf("no photo: %v", err)
			}
		}
	}

	if *zoom {
		for i := 0; i < 9; i++ {
			delta := float64(i) * 3.0
			log.Printf("taking photo %d (at %.2f mm)", i, 170-delta)
			d, err := c.SnapJPEG(ctx, i, 192.5, 170, 170-delta)
			if err != nil {
				log.Fatalf("photo grab failed: %v", err)
			}
			if err := os.WriteFile(fmt.Sprintf("photo%d.jpg", i), d, 0777); err != nil {
				log.Fatalf("no photo: %v", err)
			}
		}
	}
}
