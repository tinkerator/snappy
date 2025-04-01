package snappy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
	LaserPower       int     `json:"laserPower"`
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
	LaserPower          int             `json:"laserPower"`
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

// NewConn confirms a new connection to the A350.
func NewConn(ip, token string) (*Conn, error) {
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
	return &Conn{
		url:          u,
		token:        token,
		connected:    true,
		readOnly:     res.ReadOnly,
		headType:     res.HeadType,
		hasEnclosure: res.HasEnclosure,
	}, nil
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

// Home homes the A350 device.
func (c *Conn) Home(ctx context.Context) error {
	if err := c.waitToMove(ctx); err != nil {
		return err
	}
	defer c.stopMoving()

	if err := c.doCode("G53"); err != nil {
		return err
	}
	if err := c.doCode("G28"); err != nil {
		return err
	}
	return nil
}
