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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"zappem.net/pub/graphics/raster"
	"zappem.net/pub/net/snappy"
)

var (
	config     = flag.String("config", "snapmaker.config", "config file")
	camOffset  = flag.Bool("set-camera-offset", false, "set camera offset for current tool to --x --y --z and exit")
	home       = flag.Bool("home", false, "home device (required after power on)")
	fan        = flag.Int("fan", -1, "enable the enclosure fan")
	led        = flag.Int("led", -1, "enable the led lighting")
	x          = flag.Float64("x", 192.5, "specify x value for location")
	y          = flag.Float64("y", 170, "specify y value for location")
	z          = flag.Float64("z", 113, "specify z value for location")
	zd         = flag.Float64("zd", 1, "zoom dz delta from --{x,y,z} for --zoom pictures")
	move       = flag.Bool("move", false, "move to the specified --x --y --z location")
	spot       = flag.Bool("spot", false, "turn on the spot laser")
	cross      = flag.Bool("cross", false, "turn on the laser cross")
	nocross    = flag.Bool("nocross", false, "turn off the laser cross")
	nospot     = flag.Bool("nospot", false, "turn off the spot laser")
	locate     = flag.Bool("locate", false, "display the current coordinates")
	photo      = flag.Bool("photo", false, "request a single photo at current location")
	snapshot   = flag.Bool("snap", false, "request single photo at previously configured --set-camera-offset")
	circle     = flag.Bool("circle", false, "request a series of photos taken in a circle around --{x,y,z}")
	zoom       = flag.Bool("zoom", false, "request a series of zoomed (by --zd) photos starting at --{x,y,z}")
	marks      = flag.Bool("marks", false, "mark all photos with targeting lines")
	setOrigin  = flag.Bool("set-origin", false, "set the workspace origin to the current location")
	gotoOrigin = flag.Bool("goto-origin", false, "move the tool head to the origin location")
	nudgeX     = flag.Float64("nudge-x", 0.0, "step this many mm in the X direction")
	nudgeY     = flag.Float64("nudge-y", 0.0, "step this many mm in the Y direction")
	nudgeZ     = flag.Float64("nudge-z", 0.0, "step this many mm in the Z direction")
	edit       = flag.String("edit", "", "comment out comma separated sets of --program lines, <n> or <n>-<m>")
	program    = flag.String("program", "", "upload and execute a program")
	park       = flag.Bool("park", false, "park the head for changing and exit")
	pause      = flag.Bool("pause", false, "pause the executing program")
	resume     = flag.Bool("resume", false, "resume the executing program")
	stop       = flag.Bool("stop", false, "stop the executing program")
	poll       = flag.Bool("poll", false, "poll running program until complete")
	dump       = flag.Bool("dump", false, "dump the last cached a350 state and exit")
)

type ToolConfig struct {
	// CameraDeltaCoords, if set, holds the (dx,dy,dz) tool offset
	// to take an in-focus centered photo relative to the initial
	// tool head location. The --snap argument will nudge the head
	// by this amount, take a photo, and then un-nudge the head by
	// that same amount.
	CameraDeltaCoords []float64 `json:"CameraCoordsDelta,omitempty"`
}

// Config holds the static config for the target printer. See the
// --config option for overriding its default location.
type Config struct {
	Token   string
	Address string
	Tools   map[int]ToolConfig
}

// grep performs a regexp match in an array of []byte lines.
// Returns the number of the matching line (starts at 0), the
// string content of that line or an error.
func grep(lines [][]byte, val string) (int, string, error) {
	re, err := regexp.Compile(val)
	if err != nil {
		return 0, "", err
	}
	for n, line := range lines {
		if re.Match(line) {
			return n, string(line), nil
		}
	}
	return 0, "", fmt.Errorf("%q does not match", val)
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
	const mag = 0.75
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

func processJPEG(d []byte) []byte {
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
	return d
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

	toolID, ok, err := c.ToolHead(1)
	if err != nil {
		log.Fatalf("failed to get key=1 detail: %v", err)
	}

	if *dump {
		ms := c.ModuleList()
		log.Printf("connected modules: %#v", ms)
		c.DumpState()
		log.Printf("toolID=%d(%q) ok=%v", toolID, snappy.ModuleNames[toolID], ok)
		log.Printf("tool config: %#v", conf.Tools[toolID])
		return
	}

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
		if c.EnclosureFanNotRunning() && *fan <= 0 {
			log.Fatal("homed, but should start enclosure fan!")
		}
	}

	if *fan >= 0 && *fan <= 100 {
		log.Printf("setting enclosure --fan to %d%%", *fan)
		if err := c.EncFan(*fan); err != nil {
			log.Fatalf("unable to set enclosure fan to %d: %v", *fan, err)
		}
	}

	if *led >= 0 && *led <= 100 {
		log.Printf("setting enclosure --led to %d%%", *led)
		if err := c.EncLED(*led); err != nil {
			log.Fatalf("unable to set enclosure LED to %d: %v", *led, err)
		}
	}

	if *locate || *park {
		c.Status()
		x, y, z, ox, oy, oz := c.CurrentLocation()
		if *park {
			const tx = -179
			const ty = -327
			const tz = -156.5
			x, y, z = ox-tx, oy-ty, oz-tz
			if z < 0 {
				log.Fatalf("use --nudge-{x,y,z} instead --park would set negative z=%.2f", z)
			}
			log.Printf("parking at (%.2f,%.2f,%.2f)", x, y, z)
			if err := c.MoveTo(ctx, x, y, z); err != nil {
				log.Fatalf("park at (%.2f,%.2f,%.2f) failed: %v", x, y, z, err)
			}
			return
		}
		log.Printf("at (%.2f,%.2f,%.2f) offset=(%.2f,%.2f,%.2f)", x, y, z, ox, oy, oz)
	}

	if *camOffset {
		switch toolID {
		case 2: // TODO support 10W Laser too
		default:
			log.Fatalf("toolID=%d(%q) has no supported camera", toolID, snappy.ModuleNames[toolID])
		}
		if conf.Tools == nil {
			conf.Tools = make(map[int]ToolConfig)
		}
		tool := conf.Tools[toolID]
		tool.CameraDeltaCoords = []float64{*x, *y, *z}
		conf.Tools[toolID] = tool
		b, err := json.Marshal(conf)
		if err != nil {
			log.Fatalf("failed to marshal config: %v", err)
		}
		if err := os.WriteFile(*config, b, 0600); err != nil {
			log.Fatalf("failed to write config %q: %v", *config, err)
		}
		log.Printf("--config=%q updated with camera offset for toolID=%d(%q)", *config, toolID, snappy.ModuleNames[toolID])
		return
	}

	if *gotoOrigin {
		if err := c.GoToOrigin(ctx); err != nil {
			log.Fatalf("failed to go to origin: %v", err)
		}
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

	if *edit != "" {
		if *program == "" {
			log.Fatal("--edit requires --program to be defined")
		}
		data, err := os.ReadFile(*program)
		if err != nil {
			log.Fatalf("unable to read %q: %v", *program, err)
		}
		lines := bytes.Split(data, []byte("\n"))
		for _, sec := range strings.Split(*edit, ",") {
			nums := strings.Split(sec, "-")
			if len(nums) > 2 {
				log.Fatalf("--edit requires <n> or <n>-<m> fields; invalid: %q", sec)
			}
			from, err := strconv.Atoi(nums[0])
			if err != nil {
				log.Fatalf("failed to parse --edit=..%q..: %v", nums[0])
			}
			if from > len(lines) {
				log.Fatalf("%q is out of bounds for %q (length=%d)", sec, *program, len(lines))
			}
			to := from
			if len(nums) == 2 {
				to, err = strconv.Atoi(nums[1])
				if err != nil {
					if nums[1] != "" {
						log.Fatalf("failed to parse 2nd number from --edit=..%q..: %v", sec, err)
					}
					to = len(lines)
				}
				if to < from {
					log.Fatalf("--edit range is b>=a, not %d", sec)
				}
				if to > len(lines) {
					log.Fatalf("--edit range beyond length of --program %q vs %d", sec, len(lines))
				}
			}
			for i := from - 1; i < to; i++ {
				line := lines[i]
				if len(line) == 0 {
					continue
				}
				if bytes.HasPrefix(line, []byte(";")) {
					continue
				}
				lines[i] = append([]byte(";"), lines[i]...)
			}
		}
		replacement := bytes.Join(lines, []byte("\n"))
		output := fmt.Sprint("edited-", filepath.Base(*program))
		if err := os.WriteFile(output, replacement, 0666); err != nil {
			log.Fatalf("failed to write edited program %q: %v", output, err)
		}
		return
	}
	if *program != "" {
		data, err := os.ReadFile(*program)
		if err != nil {
			log.Fatalf("unable to read %q: %v", *program, err)
		}
		lines := bytes.Split(data, []byte("\n"))
		n, line, err := grep(lines, "^;estimated_time")
		if err != nil {
			log.Printf("no estimated time for completion [%d]: %v", n, err)
		} else if val, err := strconv.ParseFloat(line[20:], 64); err != nil {
			log.Printf("failed to parse line=%d %q: %v", n, line, err)
		} else {
			when := time.Now().Add(time.Microsecond * time.Duration(1e6*val))
			log.Printf("ETA for completion from file: %s", when.Format(time.DateTime))
		}
		if err := c.RunProgram(*program, data); err != nil {
			log.Fatalf("failed to upload and run %q: %v", *program, err)
		}
		if !*poll {
			return
		}
		log.Println("[waiting to start]")
		if err := c.Await(ctx, "RUNNING"); err != nil {
			log.Fatalf("waiting to start running failed: %v", err)
		}
	}
	if *poll {
		log.Println("[waiting for idle]")
		done := make(chan struct{})
		ready := make(chan struct{})
		go func() {
			defer close(ready)
			polled := false
			for {
				select {
				case <-time.After(3 * time.Second):
					ok, status := c.Running()
					fmt.Printf("\r%s\033[0K", status)
					polled = true
					if !ok {
						fmt.Println()
						return
					}
				case <-done:
					if polled {
						fmt.Println()
					}
					return
				}
			}
		}()
		if err := c.Await(ctx, "IDLE"); err != nil {
			log.Fatalf("waiting for idle failed: %v", err)
		}
		close(done)
		<-ready
		log.Println("[system is idle]")
		return
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
		c.Status()
		x, y, z, ox, oy, oz := c.CurrentLocation()
		log.Printf("was at (%.2f,%.2f,%.2f) offset=(%.2f,%.2f,%.2f)", x, y, z, ox, oy, oz)
		if err := c.SetOrigin(ctx); err != nil {
			log.Fatalf("failed to set origin: %v", err)
		}
		if err := c.Await(ctx, "IDLE"); err != nil {
			log.Fatalf("waiting for idle failed: %v", err)
		}
		c.Status()
		x, y, z, ox, oy, oz = c.CurrentLocation()
		log.Printf("now at (%.2f,%.2f,%.2f) offset=(%.2f,%.2f,%.2f)", x, y, z, ox, oy, oz)
	}

	if *nospot {
		if err := c.LaserSpot(ctx, 0); err != nil {
			log.Fatalf("failed to disable laser spot: %v", err)
		}
	} else if *spot {
		if err := c.LaserSpot(ctx, 1.0); err != nil {
			log.Fatalf("failed to enable laser spot: %v", err)
		}
	}

	if *nocross {
		if err := c.LaserCrossHairs(ctx, false); err != nil {
			log.Fatalf("laser cross failed to disable: %v", err)
		}
	} else if *cross {
		if err := c.LaserCrossHairs(ctx, true); err != nil {
			log.Fatalf("laser cross failed to enable: %v", err)
		}
	}

	if *snapshot {
		tool := conf.Tools[toolID]
		dXYZ := tool.CameraDeltaCoords
		if len(tool.CameraDeltaCoords) != 3 {
			log.Fatalf("tool=%d(%q) camera offset unknown", toolID, snappy.ModuleNames[toolID])
		}
		cx, cy, cz, _, _, _ := c.CurrentLocation()
		d, err := c.SnapAtJPEG(ctx, 0, cx+dXYZ[0], cy+dXYZ[1], cz+dXYZ[2])
		if err != nil {
			log.Fatalf("--snap failed at (%g,%g,%g): %v", cx+dXYZ[0], cy+dXYZ[1], cz+dXYZ[2], err)
		}
		d = processJPEG(d)
		if err := os.WriteFile("photo.jpg", d, 0777); err != nil {
			log.Fatalf("no --snap photo: %v", err)
		}
		if err := c.MoveTo(ctx, cx, cy, cz); err != nil {
			log.Fatalf("return to (%.2f,%.2f,%.2f) failed: %v", cx, cy, cz, err)
		}
		return
	}

	if *photo {
		d, err := c.SnapJPEG(ctx, 0)
		if err != nil {
			log.Fatalf("photo grab failed: %v", err)
		}
		d = processJPEG(d)
		if err := os.WriteFile("photo.jpg", d, 0777); err != nil {
			log.Fatalf("no photo: %v", err)
		}
		return
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
		return
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
		return
	}
}
