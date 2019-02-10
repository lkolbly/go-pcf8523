package pcf8523_test

import (
	"fmt"
	//"time"

	"github.com/lkolbly/go-pcf8523"
	"periph.io/x/periph/host"
)

func Example() {
	// Initialize periph.io
	host.Init()

	// Open a device
	d,err := pcf8523.NewPcf8523("/dev/i2c-1", 0x68)
	if err != nil {
		panic(err)
	}
	defer d.Close()

	// Set the time
	//d.SetTime(time.Now())

	// Enable battery switchover & low battery detection
	if err := d.ConfigurePowerManagement(true, false, true); err != nil {
		panic(err)
	}

	// Check if the battery is low
	batlow,err := d.IsBatteryLow()
	if err != nil {
		panic(err)
	}
	if batlow {
		fmt.Println("Battery LOW!")
	} else {
		fmt.Println("Battery OK")
	}

	// Get the current time
	current_time, err := d.GetTime()
	if err != nil {
		panic(err)
	}
	fmt.Printf("Current time is %v\n", current_time)
}
