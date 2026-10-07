# Xencelabs Quick Keys Driver for Linux (Go)

A lightweight, userland driver for the Xencelabs Quick Keys remote, written in Go. This driver uses `uinput` to simulate keyboard events at the kernel level, making it fully compatible with **Wayland**, **X11**, and **TTYs**.

## Features

- **Full Remapping:** Map buttons and the wheel to any keyboard combination.
- **Layer Support:** Create multiple layers (e.g., General, DaVinci, Blender) and cycle through them.
- **OLED Control:** Custom text labels per button and temporary text overlays when switching layers.
- **LED Control:** Custom colored LED ring per layer.
- **Wheel Settings:** Configurable sensitivity/speed per layer.
- **Battery Monitoring:** Reports battery percentage to the log.

## Prerequisites

You need the C headers for USB and HID libraries to compile the project.

**Debian/Ubuntu:**
```bash
sudo apt install libusb-1.0-0-dev libudev-dev pkg-config
```

## Installation

On Arch Linux and derivatives, the driver is packaged in the
[AUR](https://aur.archlinux.org/packages/xencelabs-quick-keys-go):

```sh
paru -S xencelabs-quick-keys-go
```

Otherwise, build from source.

1. **Clone the repository** (or create the files):
   ```bash
   go build .
   ```

## Configuration (`config.yaml`)

The behavior of the device is defined in `config.yaml`.

### Button Layout
```text
[0]  [1]
[2]  [3]
[4]  [5]
[6]  [7]

[8] = Physical button 
[9] = Wheel Center Click
```

### Example Config Fragment
```yaml
device:
  brightness: medium      # off, low, medium, full
  orientation: 0          # 0, 90, 180, 270
  wheel_speed: normal     # global default
  overlay_duration: 2     # seconds

layers:
  - name: "General"
    color: { r: 0, g: 255, b: 255 } # Cyan Ring
    wheel_speed: fastest            # Override speed for this layer
    buttons:
      0:
        label: "Copy"
        keys: ["KEY_LEFTCTRL", "KEY_C"]
      8:
        label: "Layer"
        keys: ["INTERNAL_LAYER_CYCLE"] # Special command to switch layers
    wheel:
      left: ["KEY_VOLUMEDOWN"]
      right: ["KEY_VOLUMEUP"]
```

## Running the Driver

### 1. Udev Rules (Required)
Allow non-root access to the raw HID device. Create `/etc/udev/rules.d/50-xencelabs.rules`:

See the example file in the repo.

Reload rules: `sudo udevadm control --reload-rules && sudo udevadm trigger`

### 2. Manual Run
Since this driver creates a virtual keyboard, it requires access to `/dev/uinput`.

```bash
./xencelabs-driver
```

## Supported Key Codes

Use standard Linux input event codes in `config.yaml`. Common examples:

- `KEY_A` ... `KEY_Z`
- `KEY_1` ... `KEY_0`
- `KEY_F1` ... `KEY_F12`
- `KEY_LEFTCTRL`, `KEY_LEFTSHIFT`, `KEY_LEFTALT`, `KEY_LEFTMETA` (Windows/Command)
- `KEY_ENTER`, `KEY_ESC`, `KEY_TAB`, `KEY_SPACE`, `KEY_BACKSPACE`
- `KEY_UP`, `KEY_DOWN`, `KEY_LEFT`, `KEY_RIGHT`
- `KEY_PAGEUP`, `KEY_PAGEDOWN`, `KEY_HOME`, `KEY_END`
- `KEY_VOLUMEDOWN`, `KEY_VOLUMEUP`, `KEY_MUTE`

## Known Bugs

None currently known.
