package snappy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNotConnected etc are errors returned by this package.
var (
	ErrNotConnected = errors.New("not connected")
	ErrInvalidToken = errors.New("invalid token")
	ErrNoKey        = errors.New("no key found")
	ErrCanceled     = errors.New("operation canceled")
	ErrInvalid      = errors.New("invalid value")
	ErrNoCamera     = errors.New("no camera")
)

// Module provides basic status information describing the keyed
// module.
type Module struct {
	Key      int  `json:"key"`
	ModuleID int  `json:"moduleId"`
	Status   bool `json:"status"`
}

var ModuleNames = map[int]string{
	1:  "standardCNCToolheadForSM2",   // CNC default tool 50W
	2:  "levelOneLaserToolheadForSM2", // Blue Laser 1.6W
	23: "2W Laser Module",             // IR Laser 2W
}

// ModuleListing is used to indicate the operational status of the
// various devices plugged into the controller.
type ModuleListing struct {
	ModuleList []Module `json:"moduleList"`
}

// EnclosureResult holds a response to the enclosure query of the A350
// machine.
type EnclosureResult struct {
	IsReady       bool `json:"isReady"`
	IsDoorEnabled bool `json:"isDoorEnabled"`
	LED           int  `json:"led"`
	Fan           int  `json:"fan"`
}

type ModuleType interface {
	MarshalJSON() ([]byte, error)
}

type ModuleDetail struct {
	Key    int `json:"key"`
	Module ModuleType
}

type ModuleLaser struct {
	LaserFocalLength float64 `json:"laserFocalLength"`
	LaserPower       float64 `json:"laserPower"`
	LaserCamera      bool    `json:"laserCamera"`
}

func (ml *ModuleLaser) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"laserFocalLength":%g,"laserPower":%g,"laserCamera":%v`, ml.LaserFocalLength, ml.LaserPower, ml.LaserCamera)), nil
}

type ModuleEnclosure struct {
	IsReady             bool `json:"isReady"`
	LED                 int  `json:"led"`
	Fan                 int  `json:"fan"`
	IsDoorEnabled       bool `json:"isDoorEnabled"`
	IsEnclosureDoorOpen bool `json:"isEnclosureDoorOpen"`
	DoorSwitchCount     int  `json:"doorSwitchCount"`
}

func (me *ModuleEnclosure) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"isReady":%v,"led":%d,"fan":%d,"isDoorEnabled":%v,"isEnclosureDoorOpen":%v,"doorSwitchCount":%d`, me.IsReady, me.LED, me.Fan, me.IsDoorEnabled, me.IsEnclosureDoorOpen, me.DoorSwitchCount)), nil
}

type ModuleEmergencyStop struct {
	IsEmergencyStopped bool `json:"isEmergencyStopped"`
}

func (mes *ModuleEmergencyStop) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"isEmergencyStopped":%v`, mes.IsEmergencyStopped)), nil
}

type ModuleQuickSwap struct {
	QuickSwapState int `json:"quickSwapState"`
	QuickSwapType  int `json:"quickSwapType"`
}

func (mq *ModuleQuickSwap) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"quickSwapState":%d,"quickSwapType":%d`, mq.QuickSwapState, mq.QuickSwapType)), nil
}

type ModuleBracingKit struct {
	BracingKitState int `json:"bracingKitState"`
}

func (mb *ModuleBracingKit) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"bracingKitState":%d`, mb.BracingKitState)), nil
}

type ModuleCNC struct {
	SpindleSpeed int `json:"spindleSpeed"`
}

func (mc *ModuleCNC) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"spindleSpeed":%d`, mc.SpindleSpeed)), nil
}

func (m ModuleDetail) String() string {
	j, err := m.MarshalJSON()
	if err != nil {
		return fmt.Sprintf("error:%v", err)
	}
	return string(j)
}

func (m *ModuleDetail) MarshalJSON() ([]byte, error) {
	b, err := m.Module.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf(`{"key":%d,%s}`, m.Key, string(b))), nil
}

func (m *ModuleDetail) UnmarshalJSON(b []byte) error {
	if !bytes.HasPrefix(b, []byte(`{"key":`)) {
		return ErrNoKey
	}
	i := bytes.Index(b[7:], []byte(","))
	if i < 0 {
		return ErrNoKey
	}
	k, err := strconv.Atoi(string(b[7 : 7+i]))
	if err != nil {
		return err
	}
	if k < 0 {
		return ErrNoKey
	}
	m.Key = k
	val := append([]byte("{"), b[7+i+1:]...)
	switch {
	case bytes.Contains(val, []byte(`"laserPower":`)):
		ml := &ModuleLaser{}
		if err := json.Unmarshal(val, ml); err != nil {
			return err
		}
		m.Module = ml
	case bytes.Contains(val, []byte(`"isEnclosureDoorOpen":`)):
		me := &ModuleEnclosure{}
		if err := json.Unmarshal(val, me); err != nil {
			return err
		}
		m.Module = me
	case bytes.Contains(val, []byte(`"isEmergencyStopped":`)):
		mes := &ModuleEmergencyStop{}
		if err := json.Unmarshal(val, mes); err != nil {
			return err
		}
		m.Module = mes
	case bytes.Contains(val, []byte(`"quickSwapState":`)):
		mq := &ModuleQuickSwap{}
		if err := json.Unmarshal(val, mq); err != nil {
			return err
		}
		m.Module = mq
	case bytes.Contains(val, []byte(`"bracingKitState":`)):
		mb := &ModuleBracingKit{}
		if err := json.Unmarshal(val, mb); err != nil {
			return err
		}
		m.Module = mb
	case bytes.Contains(val, []byte(`"spindleSpeed":`)):
		mc := &ModuleCNC{}
		if err := json.Unmarshal(val, mc); err != nil {
			return err
		}
		m.Module = mc
	default:
		log.Printf("TODO learn to unmarshal key=%d ... %s", k, val)
	}
	return nil
}

// ModResult holds a response to the module_info query of the A350
// machine.
type ModResult struct {
	ModuleInfo []ModuleDetail `json:"moduleInfo"`
}

// StatusResult holds a response to the status command.
type StatusResult struct {
	Status              string          `json:"status"`
	X                   float64         `json:"x"`
	Y                   float64         `json:"y"`
	Z                   float64         `json:"z"`
	Homed               bool            `json:"homed"`
	OffsetX             float64         `json:"offsetX"`
	OffsetY             float64         `json:"offsetY"`
	OffsetZ             float64         `json:"offsetZ"`
	ToolHead            string          `json:"toolHead"`
	LaserFocalLength    float64         `json:"laserFocalLength"`
	LaserPower          float64         `json:"laserPower"`
	LaserCamera         bool            `json:"laserCamera"`
	Laser10WErrorState  int             `json:"laser10WErrorState"`
	WorkSpeed           int             `json:"workSpeed"`
	PrintStatus         string          `json:"printStatus"`
	FileName            string          `json:"fileName"`
	TotalLines          int             `json:"totalLines"`
	EstimatedTime       float64         `json:"estimatedTime"`
	CurrentLine         int             `json:"currentLine"`
	Progress            float64         `json:"progress"`
	ElapsedTime         int             `json:"elapsedTime"`
	RemainingTime       int             `json:"remainingTime"`
	ModuleList          map[string]bool `json:"moduleList"`
	IsEnclosureDoorOpen bool            `json:"isEnclosureDoorOpen"`
	DoorSwitchCount     int             `json:"doorSwitchCount"`
}

// Conn holds connection status for Snapmaker 2.0 A350.
type Conn struct {
	url          string
	token        string
	mu           sync.Mutex
	connected    bool
	moving       bool
	readOnly     bool
	headType     int
	hasEnclosure bool
	modList      ModuleListing
	encState     EnclosureResult
	modState     ModResult
	toolState    StatusResult
}

// NowMoving registers the caller is in moving state. The function
// returns the prior state.
func (c *Conn) NowMoving(moving bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.moving
	c.moving = moving
	return old
}

// ConnectionResult holds the Snapmaker response to a new connection
// attempt.
type ConnectionResult struct {
	Token        string `json:"token"`
	ReadOnly     bool   `json:"readonly"`
	Series       string `json:"series"`
	HeadType     int    `json:"headType"`
	HasEnclosure bool   `json:"hasEnclosure"`
}

// Close closes an open connection to the A350.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		return ErrNotConnected
	}
	v := url.Values{}
	v.Set("token", c.token)
	resp, err := http.PostForm(c.url+"/api/v1/disconnect", v)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unable to close: status=%d (%q)", resp.StatusCode, resp.Status)
	}
	c.connected = false
	return nil
}

// encStatus gets the status of the Enclosure.
func (c *Conn) encStatus() error {
	resp, err := http.Get(fmt.Sprint(c.url, "/api/v1/enclosure?token=", c.token))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	return dec.Decode(&c.encState)
}

// modStatus gets the status of the attached modules.
func (c *Conn) modListing() error {
	resp, err := http.Get(fmt.Sprint(c.url, "/api/v1/module_list?token=", c.token))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	return dec.Decode(&c.modList)
}

// modStatus gets the status of the attached modules.
func (c *Conn) modStatus() error {
	resp, err := http.Get(fmt.Sprint(c.url, "/api/v1/module_info?token=", c.token))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	return dec.Decode(&c.modState)
}

// toolStatus gets the status of the tool.
func (c *Conn) toolStatus() error {
	resp, err := http.Get(fmt.Sprint(c.url, "/api/v1/status?token=", c.token))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	return dec.Decode(&c.toolState)
}

// Status obtains the status of the machine. Based on connected
// devices, the status will be obtained for what is found.
func (c *Conn) Status() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		return ErrNotConnected
	}

	var errEnc, errMod, errTool error
	var wg sync.WaitGroup
	if c.hasEnclosure {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errEnc = c.encStatus()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		errMod = c.modStatus()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		errTool = c.toolStatus()
	}()

	wg.Wait()

	if errEnc != nil {
		return errEnc
	}
	if errMod != nil {
		return errMod
	}
	return errTool
}

func (c *Conn) pollStatus(ctx context.Context) (err error) {
	once := make(chan struct{})
	go func() {
		var done bool
		if err = c.modListing(); err != nil {
			close(once)
			return
		}
		for {
			err2 := c.Status()
			if !done {
				err = err2
				close(once)
				done = true
			}
			if err2 != nil {
				log.Fatalf("c.Status returned error: %v", err2)
				continue
			}
			select {
			case <-time.After(1 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}()
	<-once
	return
}

// NewConn confirms a new connection to the A350.
func NewConn(ctx context.Context, ip, token string) (*Conn, error) {
	u := fmt.Sprintf("http://%s:8080", ip)
	v := url.Values{}
	v.Set("token", token)
	resp, err := http.PostForm(u+"/api/v1/connect", v)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed with status=%d (%q)", resp.StatusCode, resp.Status)
	}
	j := json.NewDecoder(resp.Body)
	var res ConnectionResult
	if err := j.Decode(&res); err != nil {
		return nil, err
	}
	if token != res.Token {
		return nil, ErrInvalidToken
	}
	if res.Series != "Snapmaker 2.0 A350" {
		return nil, fmt.Errorf("unsupported series %q", res.Series)
	}
	c := &Conn{
		url:          u,
		token:        token,
		connected:    true,
		readOnly:     res.ReadOnly,
		headType:     res.HeadType,
		hasEnclosure: res.HasEnclosure,
	}
	return c, c.pollStatus(ctx)
}

// Homed indicates that the A350 considers itself homed.
func (c *Conn) Homed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.toolState.Homed
}

// waitToMove waits until the device is ready to be commanded to move.
func (c *Conn) waitToMove(ctx context.Context) error {
	started := false
	for {
		c.mu.Lock()
		able := c.connected
		if able && !c.moving {
			c.moving = true
			started = true
		}
		c.mu.Unlock()
		if !able {
			return ErrNotConnected
		}
		if started {
			return nil
		}
		select {
		case <-time.After(1 * time.Millisecond):
		case <-ctx.Done():
			return ErrCanceled
		}
	}
}

// Await waits for the tool-status to become status.
func (c *Conn) Await(ctx context.Context, status string) error {
	for {
		c.mu.Lock()
		st := c.toolState.Status
		c.mu.Unlock()
		if st == status {
			break
		}
		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return ErrCanceled
		}
	}
	return nil
}

// doCode executes some G-Code on the device via a POST method.
func (c *Conn) doCode(codes string) error {
	c.mu.Lock()
	able := c.connected
	c.mu.Unlock()
	if !able {
		return ErrNotConnected
	}

	v := url.Values{}
	v.Set("token", c.token)
	v.Set("code", codes)
	resp, err := http.PostForm(c.url+"/api/v1/execute_code", v)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("code execution failure: status=%d (%q)", resp.StatusCode, resp.Status)
	}
	return nil
}

// stopMoving registers that the device is no longer being moved.
func (c *Conn) stopMoving() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.moving = false
}

// Execute a sequence of codes while the caller holds the moving semaphore.
func (c *Conn) doCodes(codes ...string) error {
	for _, code := range codes {
		if err := c.doCode(code); err != nil {
			return err
		}
	}
	return nil
}

// Home homes the A350 device.
func (c *Conn) Home(ctx context.Context) error {
	if err := c.waitToMove(ctx); err != nil {
		return err
	}
	defer c.stopMoving()
	return c.doCodes("G53", "G28")
}

// SetOrigin sets the work origin of the A350 device. After executing
// this command, (0,0,0) become the coordinates of the current
// physical position. The location of the machine origin is visible in
// the Offset{X,Y,Z} Status variables.
func (c *Conn) SetOrigin(ctx context.Context) error {
	if err := c.waitToMove(ctx); err != nil {
		return err
	}
	defer c.stopMoving()
	return c.doCodes("G92 X0 Y0 Z0")
}

// GoToOrigin moves the tool to the origin of the workspace.
func (c *Conn) GoToOrigin(ctx context.Context) error {
	if err := c.waitToMove(ctx); err != nil {
		return err
	}
	defer c.stopMoving()
	c.mu.Lock()
	z := c.toolState.Z
	c.mu.Unlock()
	if z < 0 {
		return c.doCodes("G0 F1500 Z0", "G0 X0 Y0")
	} else {
		return c.doCodes("G0 F1500 X0 Y0", "G0 Z0")
	}
}

// Move to an absolute (x,y,z) location in the current coordinates.
func (c *Conn) MoveTo(ctx context.Context, x, y, z float64) error {
	if err := c.waitToMove(ctx); err != nil {
		return err
	}
	defer c.stopMoving()
	err := c.doCodes(fmt.Sprintf("G0 F1500 X%.2f Y%.2f Z%.2f", x, y, z))
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.toolState.X = x
	c.toolState.Y = y
	c.toolState.Z = z
	c.mu.Unlock()
	return nil
}

// Step moves a relative step from the current location.
func (c *Conn) Step(ctx context.Context, dx, dy, dz float64) error {
	if err := c.waitToMove(ctx); err != nil {
		return err
	}
	defer c.stopMoving()
	err := c.doCodes("G91", fmt.Sprintf("G0 F1500 X%.2f Y%.2f Z%.2f", dx, dy, dz), "G90")
	if err != nil {
		return err
	}
	// TODO this is not working reliably.
	c.Status()
	return err
}

// LaserSpot sets the current laser power to percent.
func (c *Conn) LaserSpot(ctx context.Context, power float64) error {
	if power < 0 || power > 1.5 {
		return ErrInvalid
	}
	if err := c.waitToMove(ctx); err != nil {
		return err
	}
	defer c.stopMoving()
	return c.doCodes(fmt.Sprintf("M3 P%d S%.2f", int(power), 255*(power/100)))
}

// LaserCrossHairs sets the cross-hair targeting sight on.
func (c *Conn) LaserCrossHairs(ctx context.Context, enable bool) error {
	if err := c.waitToMove(ctx); err != nil {
		return err
	}
	defer c.stopMoving()
	on := 1
	if !enable {
		on = 0
	}
	return c.doCodes(fmt.Sprintf("M2002 T3 P%d", on))
}

// SnapAtJPEG takes a photo (index=0...8) at absolute location (x,y,z).
func (c *Conn) SnapAtJPEG(ctx context.Context, index int, x, y, z float64) ([]byte, error) {
	c.mu.Lock()
	hasCamera := c.toolState.LaserCamera
	c.mu.Unlock()
	if !hasCamera {
		return nil, ErrNoCamera
	}
	if err := c.waitToMove(ctx); err != nil {
		return nil, err
	}
	resp, err := http.Get(fmt.Sprintf("%s/api/request_capture_photo?index=%d&x=%.3f&y=%.3f&z=%.3f&feedRate=3000&photoQuality=31", c.url, index, x, y, z))
	if err != nil {
		c.stopMoving()
		return nil, err
	}
	resp.Body.Close()
	c.stopMoving()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("capture[%d] = %q(%d)", index, resp.Status, resp.StatusCode)
	}
	resp, err = http.Get(fmt.Sprintf("%s/api/get_camera_image?index=%d", c.url, index))
	defer resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image[%d] = %q(%d)", index, resp.Status, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// SnapJPEG takes a photo (index=0...8) at the current location.
func (c *Conn) SnapJPEG(ctx context.Context, index int) ([]byte, error) {
	c.mu.Lock()
	x, y, z := c.toolState.X, c.toolState.Y, c.toolState.Z
	c.mu.Unlock()
	return c.SnapAtJPEG(ctx, index, x, y, z)
}

// EncFan sets the speed of the enclosure fan to a percent of its
// rated speed.
func (c *Conn) EncFan(speed int) error {
	if speed < 0 || speed > 100 {
		return ErrInvalid
	}
	v := url.Values{}
	v.Set("token", c.token)
	v.Set("fan", fmt.Sprint(speed))
	resp, err := http.PostForm(c.url+"/api/v1/enclosure", v)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unable to adjust enclosure fan: status=%d (%q)", resp.StatusCode, resp.Status)
	}
	return nil
}

// EncLED sets the speed of the enclosure LED to a percent of its
// rated speed.
func (c *Conn) EncLED(led int) error {
	if led < 0 || led > 100 {
		return ErrInvalid
	}
	v := url.Values{}
	v.Set("token", c.token)
	v.Set("led", fmt.Sprint(led))
	resp, err := http.PostForm(c.url+"/api/v1/enclosure", v)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unable to adjust enclosure LED: status=%d (%q)", resp.StatusCode, resp.Status)
	}
	return nil
}

// CurrentLocation returns the current coordinates from the most
// recent Status update.
func (c *Conn) CurrentLocation() (x, y, z, ox, oy, oz float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	x, y, z = c.toolState.X, c.toolState.Y, c.toolState.Z
	ox, oy, oz = c.toolState.OffsetX, c.toolState.OffsetY, c.toolState.OffsetZ
	return
}

// DumpState dumps all of the current status to the log.
func (c *Conn) DumpState() {
	c.mu.Lock()
	log.Printf("encState: %#v", c.encState)
	log.Printf("modState: %v", c.modState)
	log.Printf("toolState: %#v", c.toolState)
	c.mu.Unlock()
	ok, status := c.Running()
	log.Printf("(%v): %s", ok, status)
}

// Running confirms a program is running returning summary statistics.
func (c *Conn) Running() (ok bool, result string) {
	c.mu.Lock()
	status := c.toolState.Status
	file := c.toolState.FileName
	printing := c.toolState.PrintStatus
	totLines := c.toolState.TotalLines
	curLines := c.toolState.CurrentLine
	elapsed := c.toolState.ElapsedTime
	remaining := c.toolState.RemainingTime
	c.mu.Unlock()
	if totLines == 0 {
		result = "nothing running"
		return
	}
	end := time.Now().Add(time.Duration(remaining) * time.Second)
	result = fmt.Sprintf("%s %q %s %d/%d (%d%%) %.0v ETA %s", status, file, printing, curLines, totLines, (100*curLines)/totLines, time.Microsecond*time.Duration(1e6*elapsed), end.Format(time.DateTime))
	ok = printing == "Printing"
	return
}

// RunProgram uploads a program and runs it. It may be subsequently
// PauseProgram()d and/or StopProgram()d.
func (c *Conn) RunProgram(name string, data []byte) error {
	buf := &bytes.Buffer{}
	wr := multipart.NewWriter(buf)

	part, err := wr.CreateFormField("token")
	if err != nil {
		return err
	}
	part.Write([]byte(c.token))

	ct := "application/octet-stream"
	content := "Laser"
	c.mu.Lock()
	if strings.Contains(c.toolState.ToolHead, "_CNC_") {
		content = "CNC"
	}
	c.mu.Unlock()

	part, err = wr.CreateFormField("type")
	if err != nil {
		return err
	}
	part.Write([]byte(content))

	mh := make(textproto.MIMEHeader)
	mh.Set("Content-Disposition", fmt.Sprintf("form-data; name=\"file\"; filename=%q", filepath.Base(name)))
	mh.Set("Content-Type", ct)
	part, err = wr.CreatePart(mh)
	if err != nil {
		return err
	}
	part.Write(data)
	if err = wr.Close(); err != nil {
		return err
	}

	resp, err := http.Post(c.url+"/api/v1/prepare_print", "multipart/form-data; boundary="+wr.Boundary(), buf)
	if err != nil {
		return err
	}
	result, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("prep failed with %s: %v", string(result), err)
		return fmt.Errorf("unable to prepare %q: %s", name, resp.Status)
	}

	v := url.Values{}
	v.Set("token", c.token)
	resp, err = http.PostForm(c.url+"/api/v1/start_print", v)
	if err != nil {
		return err
	}
	result, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("run failed with %s: %v", string(result), err)
		return fmt.Errorf("unable to run program %q: %s", name, resp.Status)
	}
	return nil
}

// PauseProgram pauses (see RestartProgram) the current running program.
func (c *Conn) PauseProgram() error {
	v := url.Values{}
	v.Set("token", c.token)
	resp, err := http.PostForm(c.url+"/api/v1/pause_print", v)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unable to pause program: %v", err)
	}
	return nil
}

// ResumeProgram resumes a program from the point of it being Paused.
func (c *Conn) ResumeProgram() error {
	v := url.Values{}
	v.Set("token", c.token)
	resp, err := http.PostForm(c.url+"/api/v1/resume_print", v)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unable to resume program: %v", resp.Status)
	}
	return nil
}

// StopProgram terminates the current program job. Care is needed to
// continue to use the A350 given its ambiguous resulting state.
func (c *Conn) StopProgram() error {
	v := url.Values{}
	v.Set("token", c.token)
	resp, err := http.PostForm(c.url+"/api/v1/stop_print", v)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unable to stop program: %s", resp.Status)
	}
	return nil
}

// EnclosureFanNotRunning indicates that there is an enclosure fan
// that is not yet running. All other conditions return false.
func (c *Conn) EnclosureFanNotRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.encState.IsReady {
		return false
	}
	return c.encState.Fan == 0
}

// ToolHead returns the key tool ID and its status value.
// The main tool head is key=1.
func (c *Conn) ToolHead(key int) (id int, ok bool, err error) {
	for _, m := range c.modList.ModuleList {
		if m.Key == key {
			return m.ModuleID, m.Status, nil
		}
	}
	return -1, false, ErrNoKey
}
