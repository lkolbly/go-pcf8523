package pcf8523

import (
	"errors"
	"time"

	"periph.io/x/periph/conn/i2c"
	"periph.io/x/periph/conn/i2c/i2creg"
)

type Pcf8523 struct {
	Device i2c.Dev
	bus i2c.BusCloser
}

func (p *Pcf8523) Close() {
	p.bus.Close()
}

// Reads a single register at the given address
func (p *Pcf8523) ReadReg(address byte) (byte, error) {
	w := []byte{address}
	r := make([]byte, 1)
	if err := p.Device.Tx(w, r); err != nil {
		return 0x00, err
	}
	return r[0], nil
}

// Sets a single register at the given address
func (p *Pcf8523) WriteReg(address, value byte) error {
	w := []byte{address, value}
	return p.Device.Tx(w, []byte{})
}

// Configures power management and clears all other flags in the third config register.
// The first argument configures whether switching over to battery is enabled.
// The second argument should be true if switching should happen when Vdd < Vbat
// The third argument enables whether battery low detection is enabled. The battery status can be checked by calling IsBatteryLow.
func (p *Pcf8523) ConfigurePowerManagement(switchover, directSwitchingMode, batteryLowDetection bool) error {
	value := 0
	if directSwitchingMode {
		value |= 0x1
	}
	if !switchover {
		value |= 0x2
	}
	if !batteryLowDetection {
		value |= 0x4
	}
	if value == 0x6 {
		return errors.New("Invalid power management configuration")
	}
	return p.WriteReg(0x02, byte(value))
}

// Returns true if the BLF flag is set.
func (p *Pcf8523) IsBatteryLow() (bool, error) {
	val, err := p.ReadReg(0x02)
	return val&0x4 != 0, err
}

func (p *Pcf8523) getCorrection() (int8, error) {
	c, err := p.ReadReg(0xE)
	if err != nil {
		return 0, err
	}

	// Sign extend the value. Overwrite the high bit with bit 6
	if c & 0x40 != 0 {
		c |= 0x80
	} else {
		c &= 0x7F
	}
	return int8(c), nil
}

// Adds the given time correction in secondsPerDay to the offset register. Note that this
// function will read the current time correction from the register and then add it to
// the provided value - it does not directly set the time correction register. It will
// coerce the provided value to the nearest 0.375 seconds/day, and force the result into
// the range [-24,23.62] seconds per day.
//
// For example, if you add 10 seconds per day and the device was already configured to
// 20 seconds per day, then the register will be set to 23.62 seconds per day.
//
// This function always uses the two hour correction mode.
func (p *Pcf8523) AddTimeCorrection(secondsPerDay float64) error {
	// 1 (second/day) = 11.57407PPM
	// 1 LSB = 4.34PPM (in 2 hour mode)
	offset := int8(secondsPerDay * 11.57407 / 4.34)
	current_correction, err := p.getCorrection()
	if err != nil {
		return err
	}
	new_correction := int8(current_correction) + offset
	if new_correction > 63 {
		new_correction = 63
	}
	if new_correction < -64 {
		new_correction = -64
	}
	return p.WriteReg(0xE, byte(new_correction) & 0x7F)
}

// Sets the time correction register to zero.
//
// If you want to set the time correction register to a specific value, call this register
// before calling AddTimeCorrection
func (p *Pcf8523) ResetTimeCorrection() error {
	return p.WriteReg(0xE, 0x00)
}

func parseBcd(value byte) int {
	return int((value & 0xF) + (((value >> 4) & 0xF) * 10))
}

func encodeBcd(value int) byte {
	return byte((value % 10) | ((value / 10) << 4))
}

// Read the time. This function reads all the registers at once, so the result is guaranteed
// to be coherent.
//
// The time stored on the module is assumed to be in UTC and between the years 2000 and 2100
func (p *Pcf8523) GetTime() (time.Time, error) {
	w := []byte{0x03}
	r := make([]byte, 7)
	if err := p.Device.Tx(w, r); err != nil {
		return time.Time{}, err
	}
	return time.Date(
		2000 + parseBcd(r[6]),
		time.Month(parseBcd(r[5])),
		parseBcd(r[3]),
		parseBcd(r[2]),
		parseBcd(r[1]),
		parseBcd(r[0]) & 0x7F, // The high bit is the oscillator stop flag
		0, // Nanoseconds
		time.UTC,
	), nil
}

// Sets the time. All registers are written in a single transaction, so the time is
// guaranteed to be set coherently.
//
// Assumes the input year is between 2000 and 2100, and converts the provided time to UTC
func (p *Pcf8523) SetTime(date time.Time) error {
	date = date.In(time.UTC)

	// Set the time
	w := []byte{
		0x03,
		encodeBcd(date.Second()),
		encodeBcd(date.Minute()),
		encodeBcd(date.Hour()),
		encodeBcd(date.Day()),
		encodeBcd(int(date.Weekday())),
		encodeBcd(int(date.Month())),
		encodeBcd(date.Year() - 2000),
	}
	r := []byte{}
	if err := p.Device.Tx(w, r); err != nil {
		return err
	}

	// Clear the OS flag
	return nil
}

// Creates a new PCF8523 device at the given I2C address on the given bus.
//
// Tries to clear the oscillator stop (OS) flag. If clearing it fails, returns an error.
func NewPcf8523(path string, i2caddr uint16) (Pcf8523, error) {
	b, err := i2creg.Open(path)
	if err != nil {
		return Pcf8523{}, err
	}

	d := i2c.Dev{Addr: i2caddr, Bus: b}
	p := Pcf8523{bus: b, Device: d}

	// Check the oscillator state
	seconds_reg,err := p.ReadReg(0x03)
	if err != nil {
		return Pcf8523{}, err
	}
	if seconds_reg&0x80 != 0 {
		// Try to clear it, write back the number of seconds
		if p.WriteReg(0x03, seconds_reg & 0x7F) != nil {
			return Pcf8523{}, err
		}

		// If it's still dead, fail
		seconds_reg,err := p.ReadReg(0x03)
		if err != nil {
			return Pcf8523{}, err
		}
		if seconds_reg&0x80 != 0 {
			return Pcf8523{}, errors.New("PCF8523 oscillator stopped")
		}
	}

	return p, nil
}
