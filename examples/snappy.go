// Program snappy is a demonstration command line utility to drive a
// Snapmaker 2.0 A350.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log"
	"math"
	"os"

	"zappem.net/pub/graphics/raster"
	"zappem.net/pub/net/snappy"
)

var (
	config     = flag.String("config", "snapmaker.config", "config file")
	home       = flag.Bool("home", false, "home device (required after power on)")
	fan        = flag.Int("fan", -1, "enable the enclosure fan")
	led        = flag.Int("led", -1, "enable the led lighting")
	x          = flag.Float64("x", 192.5, "specify x value for location")
	y          = flag.Float64("y", 170, "specify y value for location")
	z          = flag.Float64("z", 113, "specify z value for location")
	zd         = flag.Float64("zd", 1, "zoom dz delta from --{x,y,z} for --zoom pictures")
	move       = flag.Bool("move", false, "move to the specified --x --y --z location")
	spot       = flag.Bool("spot", false, "turn on the spot laser for photo")
	nospot     = flag.Bool("nospot", false, "turn off the spot laser")
	locate     = flag.Bool("locate", false, "display the current coordinates")
	photo      = flag.Bool("photo", false, "request a single photo at current location")
	circle     = flag.Bool("circle", false, "request a series of photos taken in a circle around --{x,y,z}")
	zoom       = flag.Bool("zoom", false, "request a series of zoomed (by --zd) photos starting at --{x,y,z}")
	marks      = flag.Bool("marks", false, "mark all photos with targeting lines")
	setOrigin  = flag.Bool("set-origin", false, "set the workspace origin to the current location")
	gotoOrigin = flag.Bool("goto-origin", false, "move the tool head to the origin location")
	nudgeX     = flag.Float64("nudge-x", 0.0, "step this many mm in the X direction")
	nudgeY     = flag.Float64("nudge-y", 0.0, "step this many mm in the Y direction")
	nudgeZ     = flag.Float64("nudge-z", 0.0, "step this many mm in the Z direction")
	program    = flag.String("program", "", "upload and execute a program")
	pause      = flag.Bool("pause", false, "pause the executing program")
	resume     = flag.Bool("resume", false, "resume the executing program")
	stop       = flag.Bool("stop", false, "stop the executing program")
	poll       = flag.Bool("poll", false, "poll running program until complete")
)

type Config struct {
	Token   string
	Address string
}

// markUp overlays some targeting lines on an image.
func markUp(jp []byte) (draw.Image, error) {
	buf := bytes.NewBuffer(jp)
	im, format, err := image.Decode(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode %q image: %v", format, err)
	}
	bb := im.Bounds()
	w := bb.Max.X - bb.Min.X
	if h := bb.Max.Y - bb.Min.Y; h < w {
		w = h
	}
	r := .1 * float64(w)
	pen := raster.NewRasterizer()
	const delta = 3.0
	const wide = 2.0
	const base = 4.0
	const mag = 1.08
	raster.LineTo(pen, false, base+0, base+0, base+r-delta, base+r-delta, wide)
	raster.LineTo(pen, false, base+2*r, base+2*r, base+r+delta, base+r+delta, wide)
	raster.LineTo(pen, false, base+2*r, base+0, base+r+delta, base+r-delta, wide)
	raster.LineTo(pen, false, base+0, base+2*r, base+r-delta, base+r+delta, wide)
	raster.LineTo(pen, false, base+r-r*mag, base+4*r/3, base+r-r*mag, base+2*r/3, wide/2)
	raster.LineTo(pen, false, base+r+r*mag, base+4*r/3, base+r+r*mag, base+2*r/3, wide/2)
	raster.LineTo(pen, false, base+4*r/3, base+r-r*mag, base+2*r/3, base+r-r*mag, wide/2)
	raster.LineTo(pen, false, base+4*r/3, base+r+r*mag, base+2*r/3, base+r+r*mag, wide/2)
	out := image.NewRGBA(bb)
	draw.Draw(out, bb, im, image.ZP, draw.Src)
	pen.Render(out, float64(bb.Min.X+bb.Max.X)/2-(r+base), float64(bb.Min.Y+bb.Max.Y)/2-(r+base), color.RGBA{255, 0, 255, 255})
	return out, nil
}

func main() {
	flag.Parse()

	ctx := context.Background()

	data, err := os.ReadFile(*config)
	if err != nil {
		log.Fatalf("failed to read --config=%q: %v", *config, err)
	}
	var conf Config
	if err := json.Unmarshal(data, &conf); err != nil {
		log.Fatalf("failed to import %q: %v", *config, err)
	}

	c, err := snappy.NewConn(ctx, conf.Address, conf.Token)
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

	if *home {
		if err := c.Home(ctx); err != nil {
			log.Fatalf("failed to home device: %v", err)
		}
	}

	if *gotoOrigin {
		if err := c.GoToOrigin(ctx); err != nil {
			log.Fatalf("failed to go to origin: %v", err)
		}
	}

	if *fan >= 0 && *fan <= 100 {
		if err := c.EncFan(*fan); err != nil {
			log.Printf("unable to set enclosure fan to %d: %v", *fan, err)
		}
	}

	if *led >= 0 && *led <= 100 {
		if err := c.EncLED(*led); err != nil {
			log.Printf("unable to set enclosure LED to %d: %v", *led, err)
		}
	}

	if *locate {
		c.Status()
		x, y, z, ox, oy, oz := c.CurrentLocation()
		log.Printf("at (%.2f,%.2f,%.2f) offset=(%.2f,%.2f,%.2f)", x, y, z, ox, oy, oz)
	}

	if *pause {
		if err := c.PauseProgram(); err != nil {
			log.Fatalf("failed to pause: %v", err)
		}
		return
	}

	if *resume {
		if err := c.ResumeProgram(); err != nil {
			log.Fatalf("failed to resume: %v", err)
		}
		return
	}

	if *stop {
		if err := c.StopProgram(); err != nil {
			log.Fatalf("failed to stop: %v", err)
		}
		return
	}

	if *program != "" {
		data, err := os.ReadFile(*program)
		if err != nil {
			log.Fatalf("unable to read %q: %v", *program, err)
		}
		if err := c.RunProgram(*program, data); err != nil {
			log.Fatalf("failed to upload and run %q: %v", *program, err)
		}
		if *poll {
			log.Println("need to poll for program completion")
		}
		return
	}
	if *poll {
		log.Fatalf("need to poll for program completion")
	}

	var dx, dy, dz float64
	var nudged bool
	if *nudgeX != 0 {
		dx = *nudgeX
		nudged = true
	}
	if *nudgeY != 0 {
		dy = *nudgeY
		nudged = true
	}
	if *nudgeZ != 0 {
		dz = *nudgeZ
		nudged = true
	}

	if nudged {
		if err := c.Step(ctx, dx, dy, dz); err != nil {
			log.Fatalf("nudge (%.2f,%.2f,%.2f) failed: %v", dx, dy, dz, err)
		}
	} else if *move {
		if err := c.MoveTo(ctx, *x, *y, *z); err != nil {
			log.Fatalf("move to (%.2f,%.2f,%.2f) failed: %v", *x, *y, *z, err)
		}
	}

	if *setOrigin {
		if err := c.SetOrigin(ctx); err != nil {
			log.Fatalf("failed to set origin: %v", err)
		}
	}

	if *spot {
		defer c.LaserSpot(ctx, 0)
		if err := c.LaserSpot(ctx, 1.0); err != nil {
			log.Fatalf("failed to enable laser spot: %v", err)
		}
	}

	if *photo {
		d, err := c.SnapJPEG(ctx, 0)
		if err != nil {
			log.Fatalf("photo grab failed: %v", err)
		}
		if *marks {
			im, err := markUp(d)
			if err != nil {
				log.Fatalf("failed to mark up the image: %v", err)
			}
			buf := &bytes.Buffer{}
			if err := jpeg.Encode(buf, im, nil); err != nil {
				log.Fatalf("failed to encode jpeg: %v", err)
			}
			d = buf.Bytes()
		}
		if err := os.WriteFile("photo.jpg", d, 0777); err != nil {
			log.Fatalf("no photo: %v", err)
		}
	}

	if *spot || *nospot {
		c.LaserSpot(ctx, 0)
	}

	if *circle {
		for i := 0; i < 9; i++ {
			theta := float64(i) / 9.0 * 2.0 * math.Pi
			r := 15.0
			log.Printf("taking photo %d (at %.2f deg)", i, theta/math.Pi*180)
			d, err := c.SnapAtJPEG(ctx, i, *x+r*math.Cos(theta), *y+r*math.Sin(theta), *z)
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
			delta := float64(i) * *zd
			height := *z - delta
			log.Printf("taking photo %d (at %.2f mm)", i, height)
			d, err := c.SnapAtJPEG(ctx, i, *x, *y, height)
			if err != nil {
				log.Fatalf("photo grab failed: %v", err)
			}
			if err := os.WriteFile(fmt.Sprintf("photo%d.jpg", i), d, 0777); err != nil {
				log.Fatalf("no photo: %v", err)
			}
		}
	}
}
