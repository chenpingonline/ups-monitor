package main

import (
	"bufio"
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestSplitNutHandlesQuotedAndEscapedValues(t *testing.T) {
	got := splitNut(`VAR main ups.model "Smart UPS \"Pro\""`)
	want := []string{"VAR", "main", "ups.model", `Smart UPS "Pro"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitNut() = %#v, want %#v", got, want)
	}
}

func TestNutClientListUPSAndVars(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for requestNumber := 0; requestNumber < 2; requestNumber++ {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			line, _ := bufio.NewReader(connection).ReadString('\n')
			switch line {
			case "LIST UPS\n":
				_, _ = fmt.Fprint(connection, "BEGIN LIST UPS\nUPS main \"Office UPS\"\nEND LIST UPS\n")
			case "LIST VAR main\n":
				_, _ = fmt.Fprint(connection, "BEGIN LIST VAR main\nVAR main battery.charge \"87.5\"\nEND LIST VAR main\n")
			}
			_ = connection.Close()
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	client := NutClient{Host: "127.0.0.1", Port: address.Port, Timeout: time.Second}
	upsList, err := client.ListUPS()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(upsList, []UPSInfo{{Name: "main", Description: "Office UPS"}}) {
		t.Fatalf("ListUPS() = %#v", upsList)
	}
	values, err := client.Vars("main")
	if err != nil {
		t.Fatal(err)
	}
	if values["battery.charge"] != "87.5" {
		t.Fatalf("Vars() = %#v", values)
	}
}

func TestNormalizeMapsStatusAndMetrics(t *testing.T) {
	status := normalize("main", []UPSInfo{{Name: "main"}}, map[string]string{
		"ups.status": "OL CHRG CUSTOM", "battery.charge": "87.5", "ups.load": "invalid",
		"device.model": "Fallback Model", "battery.charge.low": "20", "battery.runtime.low": "120",
		"input.voltage.nominal": "230", "ups.beeper.status": "enabled", "ups.firmware": "1234",
		"driver.version": "2.8.2", "driver.version.data": "APC HID 0.100",
	})
	if status.StatusText != "市电供电 / 充电中 / CUSTOM" {
		t.Fatalf("StatusText = %q", status.StatusText)
	}
	if status.Charge == nil || *status.Charge != 87.5 {
		t.Fatalf("Charge = %v", status.Charge)
	}
	if status.Load != nil {
		t.Fatalf("Load = %v, want nil", status.Load)
	}
	if status.UPSModel != "Fallback Model" {
		t.Fatalf("UPSModel = %q", status.UPSModel)
	}
	if status.Raw["battery.charge.low"] != "20" {
		t.Fatalf("Raw = %#v", status.Raw)
	}
	if status.ChargeLow == nil || *status.ChargeLow != 20 || status.RuntimeLow == nil || *status.RuntimeLow != 120 {
		t.Fatalf("thresholds = charge %v, runtime %v", status.ChargeLow, status.RuntimeLow)
	}
	if status.InputVoltageNominal == nil || *status.InputVoltageNominal != 230 || status.BeeperStatus != "enabled" || status.UPSFirmware != "1234" || status.DriverVersion != "2.8.2" || status.DriverDataVersion != "APC HID 0.100" {
		t.Fatalf("device metadata = %#v", status)
	}
}

func TestNormalizeIdentifiesSchneiderAPCAndEstimatesPower(t *testing.T) {
	status := normalize("main", []UPSInfo{{Name: "main"}}, map[string]string{
		"ups.status": "OL DISCHRG", "device.mfr": "American Power Conversion", "device.model": "Back-UPS BK650M2-CH",
		"ups.load": "25", "input.voltage": "232", "input.transfer.low": "160", "input.transfer.high": "278", "battery.type": "PbAc",
	})
	if status.Profile.Brand != "施耐德 APC" || status.Profile.CapacityVA != 650 || status.Profile.RatedPowerW != 390 || status.Profile.BatteryChemistry != "铅酸电池" {
		t.Fatalf("Profile = %#v", status.Profile)
	}
	if status.RealPower == nil || *status.RealPower != 97.5 || !status.RealPowerEstimated {
		t.Fatalf("RealPower = %v, estimated = %v", status.RealPower, status.RealPowerEstimated)
	}
	if status.InputTransferLow == nil || *status.InputTransferLow != 160 || status.InputTransferHigh == nil || *status.InputTransferHigh != 278 {
		t.Fatalf("transfer range = %v - %v", status.InputTransferLow, status.InputTransferHigh)
	}
}

func TestNutClientDiscoversCommandsAndWritableVars(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for requestNumber := 0; requestNumber < 2; requestNumber++ {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			line, _ := bufio.NewReader(connection).ReadString('\n')
			switch line {
			case "LIST CMD main\n":
				_, _ = fmt.Fprint(connection, "BEGIN LIST CMD main\nCMD main test.battery.start.quick\nCMD main beeper.mute\nEND LIST CMD main\n")
			case "LIST RW main\n":
				_, _ = fmt.Fprint(connection, "BEGIN LIST RW main\nRW main input.sensitivity medium\nRW main battery.charge.low 10\nEND LIST RW main\n")
			}
			_ = connection.Close()
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	client := NutClient{Host: "127.0.0.1", Port: address.Port, Timeout: time.Second}
	capabilities, err := client.Capabilities("main")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(capabilities.Commands, []string{"beeper.mute", "test.battery.start.quick"}) {
		t.Fatalf("Commands = %#v", capabilities.Commands)
	}
	if !reflect.DeepEqual(capabilities.WritableVars, []string{"battery.charge.low", "input.sensitivity"}) {
		t.Fatalf("WritableVars = %#v", capabilities.WritableVars)
	}
}

func TestInstantCommandAuthenticatesAndRestrictsCommands(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		for _, expected := range []string{"USERNAME monitor\n", "PASSWORD secret\n", "INSTCMD main test.battery.start.quick\n"} {
			line, _ := reader.ReadString('\n')
			if line != expected {
				return
			}
			_, _ = fmt.Fprint(connection, "OK\n")
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	client := NutClient{Host: "127.0.0.1", Port: address.Port, Timeout: time.Second, Username: "monitor", Password: "secret"}
	if err := client.InstantCommand("main", "test.battery.start.quick"); err != nil {
		t.Fatal(err)
	}
	<-done
	if err := client.InstantCommand("main", "load.off"); err == nil {
		t.Fatal("unsafe command unexpectedly accepted")
	}
}
