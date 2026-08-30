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
		"device.model": "Fallback Model", "battery.charge.low": "20",
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
}
