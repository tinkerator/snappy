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
)

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

func (ml ModuleLaser) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"laserFocalLength":%g,"laserPower":%d,"laserCamera":%v`, ml.LaserFocalLength, ml.LaserPower, ml.LaserCamera)), nil
}

type ModuleEnclosure struct {
	IsReady             bool `json:"isReady"`
	LED                 int  `json:"led"`
	Fan                 int  `json:"fan"`
	IsDoorEnabled       bool `json:"isDoorEnabled"`
	IsEnclosureDoorOpen bool `json:"isEnclosureDoorOpen"`
	DoorSwitchCount     int  `json:"doorSwitchCount"`
}

func (me ModuleEnclosure) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"isReady":%v,"led":%d,"fan":%d,"isDoorEnabled":%v,"isEnclosureDoorOpen":%v,"doorSwitchCount":%d`, me.IsReady, me.LED, me.Fan, me.IsDoorEnabled, me.IsEnclosureDoorOpen, me.DoorSwitchCount)), nil
}

type ModuleEmergencyStop struct {
	IsEmergencyStopped bool `json:"isEmergencyStopped"`
}

func (mes ModuleEmergencyStop) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"isEmergencyStopped":%v`, mes.IsEmergencyStopped)), nil
}

type ModuleQuickSwap struct {
	QuickSwapState int `json:"quickSwapState"`
	QuickSwapType  int `json:"quickSwapType"`
}

func (mq ModuleQuickSwap) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"quickSwapState":%d,"quickSwapType":%d`, mq.QuickSwapState, mq.QuickSwapType)), nil
}

type ModuleBracingKit struct {
	BracingKitState int `json:"bracingKitState"`
}

func (mb ModuleBracingKit) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"bracingKitState":%d`, mb.BracingKitState)), nil
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
		for {
			err2 := c.Status()
			if !done {
				err = err2
				close(once)
				done = true
			}
			if err2 != nil {
				return
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

// SnapAtJPEG takes a photo (index=0...8) at absolute location (x,y,z).
func (c *Conn) SnapAtJPEG(ctx context.Context, index int, x, y, z float64) ([]byte, error) {
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

	part, err = wr.CreateFormField("type")
	if err != nil {
		return err
	}
	part.Write([]byte(`Laser`))

	mh := make(textproto.MIMEHeader)
	mh.Set("Content-Disposition", fmt.Sprintf("form-data; name=\"file\"; filename=%q", filepath.Base(name)))
	mh.Set("Content-Type", "application/x-netcdf")
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
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unable to prepare %q: %s", name, resp.Status)
	}

	v := url.Values{}
	v.Set("token", c.token)
	resp, err = http.PostForm(c.url+"/api/v1/start_print", v)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
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
