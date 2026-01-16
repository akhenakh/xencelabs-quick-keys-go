package main

import (
	"bytes"
	"encoding/binary"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"github.com/bendahl/uinput"
	"github.com/karalabe/hid"
	"gopkg.in/yaml.v3"
)

const (
	VendorID       = 0x28BD
	ProdIDWire     = 0x5202
	ProdIDWire2    = 0x5204
	ProdIDWireless = 0x5203
)

// Configuration Structures
type Config struct {
	Device DeviceSettings `yaml:"device"`
	Layers []Layer        `yaml:"layers"`
}

type DeviceSettings struct {
	Brightness   string `yaml:"brightness"`
	Orientation  int    `yaml:"orientation"`
	WheelSpeed   string `yaml:"wheel_speed"`
	SleepTimeout uint8  `yaml:"sleep_timeout"`
}

type RGB struct {
	R uint8 `yaml:"r"`
	G uint8 `yaml:"g"`
	B uint8 `yaml:"b"`
}

type Layer struct {
	Name       string            `yaml:"name"`
	Color      RGB               `yaml:"color"`       // Layer specific color
	WheelSpeed string            `yaml:"wheel_speed"` // Layer specific wheel speed (optional)
	Buttons    map[int]ButtonCfg `yaml:"buttons"`
	Wheel      struct {
		Left  []string `yaml:"left"`
		Right []string `yaml:"right"`
	} `yaml:"wheel"`
}

type ButtonCfg struct {
	Label string   `yaml:"label"`
	Keys  []string `yaml:"keys"`
}

func padBytes(input []byte) []byte {
	out := make([]byte, 32)
	copy(out, input)
	return out
}

func encodeText(text string) []byte {
	utf16Units := utf16.Encode([]rune(text))
	buf := new(bytes.Buffer)
	for _, unit := range utf16Units {
		binary.Write(buf, binary.LittleEndian, unit)
	}
	b := buf.Bytes()
	if len(b) > 16 {
		return b[:16]
	}
	return b
}

func cmdSetKeyText(keyIndex uint8, text string) []byte {
	encoded := encodeText(text)
	lenVal := uint8(len(encoded))
	if lenVal > 16 {
		lenVal = 16
	}

	header := []byte{0x02, 0xb1, 0x00, keyIndex + 1, 0x00, lenVal}
	report := make([]byte, 32)
	copy(report[0:], header)
	copy(report[16:], encoded)
	return report
}

func cmdSetWheelColor(r, g, b uint8) []byte {
	return padBytes([]byte{0x02, 0xb4, 0x01, 0x01, 0x00, 0x00, r, g, b})
}

func cmdSetOrientation(deg int) []byte {
	val := uint8(1)
	switch deg {
	case 90:
		val = 2
	case 180:
		val = 3
	case 270:
		val = 4
	}
	return padBytes([]byte{0x02, 0xb1, val})
}

func cmdSetBrightness(level string) []byte {
	val := uint8(2) // medium
	switch strings.ToLower(level) {
	case "off":
		val = 0
	case "low":
		val = 1
	case "full":
		val = 3
	}
	return padBytes([]byte{0x02, 0xb1, 0x0a, 0x01, val})
}

func cmdSetWheelSpeed(speed string) []byte {
	val := uint8(3) // normal
	switch strings.ToLower(speed) {
	case "slowest":
		val = 5
	case "slower":
		val = 4
	case "normal":
		val = 3
	case "faster":
		val = 2
	case "fastest":
		val = 1
	}
	return padBytes([]byte{0x02, 0xb4, 0x04, 0x01, 0x01, val})
}

func cmdSleepTimeout(mins uint8) []byte {
	return padBytes([]byte{0x02, 0xb4, 0x08, 0x01, mins})
}

// msgsShowOverlayText constructs the packets needed to show text on the OLED.
// The protocol mandates at least two packets (0x05 start, 0x06 continue) for the text to appear.
func msgsShowOverlayText(duration uint8, text string) [][]byte {
	allBytes := encodeText(text)
	var packets [][]byte

	// Helper to create a 32-byte report
	createReport := func(cmd byte, dur byte, data []byte, hasMore bool) []byte {
		report := make([]byte, 32)
		report[0] = 0x02
		report[1] = 0xb1
		report[2] = cmd
		report[3] = dur
		report[4] = 0x00
		report[5] = uint8(len(data))
		if hasMore {
			report[6] = 0x01
		} else {
			report[6] = 0x00
		}

		// Copy data to index 16
		if len(data) > 0 {
			copy(report[16:], data)
		}
		return report
	}

	// Packet 1: Command 0x05 (Start). First 16 bytes (8 chars).
	// Note: Protocol reference suggests hasMore is always 0 for the 0x05 packet.
	chunk1 := allBytes
	if len(chunk1) > 16 {
		chunk1 = chunk1[:16]
	}
	packets = append(packets, createReport(0x05, duration, chunk1, false))

	// Packet 2: Command 0x06 (Continue). Next 16 bytes.
	// This packet is sent even if empty, which seems to be the trigger for the display update.
	var chunk2 []byte
	if len(allBytes) > 16 {
		chunk2 = allBytes[16:]
		if len(chunk2) > 16 {
			chunk2 = chunk2[:16]
		}
	}
	// hasMore is true if there is data beyond the first 32 bytes (16 chars)
	hasMore2 := len(allBytes) > 32
	packets = append(packets, createReport(0x06, duration, chunk2, hasMore2))

	// Remaining Packets (if text > 16 chars)
	for offset := 32; offset < len(allBytes); offset += 16 {
		end := offset + 16
		if end > len(allBytes) {
			end = len(allBytes)
		}
		chunk := allBytes[offset:end]
		hasMore := end < len(allBytes)
		packets = append(packets, createReport(0x06, duration, chunk, hasMore))
	}

	return packets
}

func main() {
	//  Load Config
	cfgData, err := os.ReadFile("config.yaml")
	if err != nil {
		log.Fatalf("Error reading config: %v", err)
	}
	var config Config
	if err := yaml.Unmarshal(cfgData, &config); err != nil {
		log.Fatalf("Error parsing YAML: %v", err)
	}

	if len(config.Layers) == 0 {
		log.Fatal("No layers defined in config")
	}

	// Setup UInput
	keyboard, err := uinput.CreateKeyboard("/dev/uinput", []byte("Xencelabs QuickKeys Virtual"))
	if err != nil {
		log.Fatalf("Failed to create virtual keyboard: %v", err)
	}
	defer keyboard.Close()

	// Connect to Device
	devInfo := findDevice()
	if devInfo == nil {
		log.Fatal("Xencelabs Quick Keys device not found")
	}

	dev, err := devInfo.Open()
	if err != nil {
		log.Fatalf("Failed to open device: %v", err)
	}
	defer dev.Close()

	log.Printf("Connected to %04x:%04x", devInfo.VendorID, devInfo.ProductID)

	// Initialize Global Settings & First Layer
	currentLayerIdx := 0
	initGlobalSettings(dev, &config)
	refreshLayer(dev, config.Layers[currentLayerIdx], config.Device.WheelSpeed, true)

	// Setup Signal Handling
	// We specifically include SIGQUIT here.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	buf := make([]byte, 32)
	var prevBtn1, prevBtn2 uint8

	// Optional: Flush/Read initial state to prevent immediate trigger on startup if a button is held
	if n, err := dev.Read(buf); err == nil && n >= 10 {
		offset := 0
		if buf[0] == 0xf0 {
			offset = -1
		}
		if buf[offset+1] == 0xf0 {
			prevBtn1 = buf[offset+2]
			prevBtn2 = buf[offset+3]
		}
	}

	// Start Input Loop
	go func() {
		for {
			n, err := dev.Read(buf)
			if err != nil {
				log.Printf("Read error: %v", err)
				break
			}
			if n < 10 {
				continue
			}

			offset := 0
			if buf[0] == 0xf0 {
				offset = -1
			}

			if buf[offset+1] == 0xf0 {
				keys1 := buf[offset+2]
				keys2 := buf[offset+3]
				wheel := buf[offset+7]

				layerChanged := handleButtons(keyboard, keys1, keys2, prevBtn1, prevBtn2, config.Layers[currentLayerIdx])

				if layerChanged {
					currentLayerIdx = (currentLayerIdx + 1) % len(config.Layers)
					log.Printf("Switching to Layer: %s", config.Layers[currentLayerIdx].Name)
					refreshLayer(dev, config.Layers[currentLayerIdx], config.Device.WheelSpeed, true)
				}

				prevBtn1 = keys1
				prevBtn2 = keys2

				if wheel > 0 {
					handleWheel(keyboard, wheel, config.Layers[currentLayerIdx])
				}
			}
		}
	}()

	// Signal Loop
	for {
		sig := <-sigs
		// FIX: Ignore SIGQUIT to prevent crash when injecting Ctrl+\
		if sig == syscall.SIGQUIT {
			log.Println("Received SIGQUIT (Ctrl+\\), ignoring because we are likely injecting it.")
			continue
		}
		log.Printf("Received signal: %v, exiting...", sig)
		return
	}
}

func findDevice() *hid.DeviceInfo {
	infos := hid.Enumerate(VendorID, 0)
	for _, info := range infos {
		if info.ProductID == ProdIDWire || info.ProductID == ProdIDWireless || info.ProductID == ProdIDWire2 {
			if info.Interface == 2 {
				return &info
			}
		}
	}
	return nil
}

// initGlobalSettings sets things that don't change between layers
func initGlobalSettings(dev *hid.Device, cfg *Config) {
	dev.Write(padBytes([]byte{0x02, 0xb0, 0x04})) // Subscribe Key events
	dev.Write(padBytes([]byte{0x02, 0xb4, 0x10})) // Subscribe Battery

	dev.Write(cmdSetOrientation(cfg.Device.Orientation))
	dev.Write(cmdSetBrightness(cfg.Device.Brightness))
	dev.Write(cmdSetWheelSpeed(cfg.Device.WheelSpeed))
	dev.Write(cmdSleepTimeout(cfg.Device.SleepTimeout))
	time.Sleep(50 * time.Millisecond)
}

// refreshLayer updates button labels, ring color, wheel speed and shows overlay
func refreshLayer(dev *hid.Device, layer Layer, globalSpeed string, showOverlay bool) {
	// Set Color
	dev.Write(cmdSetWheelColor(layer.Color.R, layer.Color.G, layer.Color.B))

	//  Set Wheel Speed (Layer overrides Global)
	targetSpeed := layer.WheelSpeed
	if targetSpeed == "" {
		targetSpeed = globalSpeed
	}
	dev.Write(cmdSetWheelSpeed(targetSpeed))

	// Set Labels (Buttons 0-7)
	for idx := 0; idx <= 7; idx++ {
		if btn, ok := layer.Buttons[idx]; ok {
			dev.Write(cmdSetKeyText(uint8(idx), btn.Label))
		} else {
			dev.Write(cmdSetKeyText(uint8(idx), "")) // Clear if not set
		}
		// Small throttling to prevent overwhelming the HID buffer
		time.Sleep(10 * time.Millisecond)
	}

	//  Show Overlay
	if showOverlay {
		pkts := msgsShowOverlayText(1, layer.Name) // Show for x seconds
		for _, p := range pkts {
			dev.Write(p)
		}
	}
}

// handleButtons returns true if a layer cycle was requested
func handleButtons(kb uinput.Keyboard, k1, k2, pk1, pk2 uint8, layer Layer) bool {
	layerCycleRequested := false

	// Check Buttons 0-7 (k1)
	for i := 0; i < 8; i++ {
		mask := uint8(1 << i)
		isPressed := (k1 & mask) > 0
		wasPressed := (pk1 & mask) > 0

		if isPressed != wasPressed {
			if cfg, ok := layer.Buttons[i]; ok {
				if isInternalCycle(cfg.Keys) {
					if isPressed {
						layerCycleRequested = true
					}
				} else {
					sendKeys(kb, cfg.Keys, isPressed)
				}
			}
		}
	}

	// Check Button 8 (Extra) - k2 Bit 0
	{
		mask := uint8(1)
		isPressed := (k2 & mask) > 0
		wasPressed := (pk2 & mask) > 0
		if isPressed != wasPressed {
			if cfg, ok := layer.Buttons[8]; ok {
				if isInternalCycle(cfg.Keys) {
					if isPressed {
						layerCycleRequested = true
					}
				} else {
					sendKeys(kb, cfg.Keys, isPressed)
				}
			}
		}
	}

	// Check Button 9 (Wheel Click) - k2 Bit 1
	{
		mask := uint8(2)
		isPressed := (k2 & mask) > 0
		wasPressed := (pk2 & mask) > 0
		if isPressed != wasPressed {
			if cfg, ok := layer.Buttons[9]; ok {
				if isInternalCycle(cfg.Keys) {
					if isPressed {
						layerCycleRequested = true
					}
				} else {
					sendKeys(kb, cfg.Keys, isPressed)
				}
			}
		}
	}

	return layerCycleRequested
}

func handleWheel(kb uinput.Keyboard, val uint8, layer Layer) {
	if val == 1 { // Right
		sendKeysPressRelease(kb, layer.Wheel.Right)
	} else if val == 2 { // Left
		sendKeysPressRelease(kb, layer.Wheel.Left)
	}
}

func isInternalCycle(keys []string) bool {
	for _, k := range keys {
		if k == "INTERNAL_LAYER_CYCLE" {
			return true
		}
	}
	return false
}

// Map string names to uinput key codes
func keyMap(name string) int {
	m := map[string]int{
		"KEY_A": uinput.KeyA, "KEY_B": uinput.KeyB, "KEY_C": uinput.KeyC,
		"KEY_D": uinput.KeyD, "KEY_E": uinput.KeyE, "KEY_F": uinput.KeyF,
		"KEY_G": uinput.KeyG, "KEY_H": uinput.KeyH, "KEY_I": uinput.KeyI,
		"KEY_J": uinput.KeyJ, "KEY_K": uinput.KeyK, "KEY_L": uinput.KeyL,
		"KEY_M": uinput.KeyM, "KEY_N": uinput.KeyN, "KEY_O": uinput.KeyO,
		"KEY_P": uinput.KeyP, "KEY_Q": uinput.KeyQ, "KEY_R": uinput.KeyR,
		"KEY_S": uinput.KeyS, "KEY_T": uinput.KeyT, "KEY_U": uinput.KeyU,
		"KEY_V": uinput.KeyV, "KEY_W": uinput.KeyW, "KEY_X": uinput.KeyX,
		"KEY_Y": uinput.KeyY, "KEY_Z": uinput.KeyZ,
		"KEY_1": uinput.Key1, "KEY_2": uinput.Key2, "KEY_3": uinput.Key3,
		"KEY_4": uinput.Key4, "KEY_5": uinput.Key5, "KEY_6": uinput.Key6,
		"KEY_7": uinput.Key7, "KEY_8": uinput.Key8, "KEY_9": uinput.Key9,
		"KEY_0":        uinput.Key0,
		"KEY_LEFTCTRL": uinput.KeyLeftctrl, "KEY_LEFTSHIFT": uinput.KeyLeftshift,
		"KEY_LEFTALT": uinput.KeyLeftalt, "KEY_TAB": uinput.KeyTab,
		"KEY_ENTER": uinput.KeyEnter, "KEY_ESC": uinput.KeyEsc,
		"KEY_BACKSPACE": uinput.KeyBackspace, "KEY_BACKSLASH": uinput.KeyBackslash,
		"KEY_MINUS": uinput.KeyMinus, "KEY_EQUAL": uinput.KeyEqual,
		"KEY_LEFTBRACE": uinput.KeyLeftbrace, "KEY_RIGHTBRACE": uinput.KeyRightbrace,
		"KEY_PAGEUP": uinput.KeyPageup, "KEY_PAGEDOWN": uinput.KeyPagedown,
		// Arrow Keys
		"KEY_LEFT": uinput.KeyLeft, "KEY_RIGHT": uinput.KeyRight,
		"KEY_UP": uinput.KeyUp, "KEY_DOWN": uinput.KeyDown,

		"KEY_VOLUMEDOWN": uinput.KeyVolumedown, "KEY_VOLUMEUP": uinput.KeyVolumeup,
		"KEY_MUTE": uinput.KeyMute, "KEY_SPACE": uinput.KeySpace,
		"KEY_F1": uinput.KeyF1, "KEY_F2": uinput.KeyF2, "KEY_F3": uinput.KeyF3,
		"KEY_F4": uinput.KeyF4, "KEY_F5": uinput.KeyF5, "KEY_F6": uinput.KeyF6,
		"KEY_F7": uinput.KeyF7, "KEY_F8": uinput.KeyF8, "KEY_F9": uinput.KeyF9,
		"KEY_F10": uinput.KeyF10, "KEY_F11": uinput.KeyF11, "KEY_F12": uinput.KeyF12,
	}
	if v, ok := m[name]; ok {
		return v
	}
	log.Printf("Unknown key: %s", name)
	return 0
}

func sendKeys(kb uinput.Keyboard, keys []string, press bool) {
	for _, kName := range keys {
		code := keyMap(kName)
		if code != 0 {
			if press {
				kb.KeyDown(code)
			} else {
				kb.KeyUp(code)
			}
		}
	}
}

func sendKeysPressRelease(kb uinput.Keyboard, keys []string) {
	for _, kName := range keys {
		code := keyMap(kName)
		if code != 0 {
			kb.KeyPress(code)
		}
	}
}
