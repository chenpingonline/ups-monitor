package main

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func readMQTTPacket(reader *bufio.Reader) (byte, []byte, error) {
	header, err := reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	remaining, multiplier := 0, 1
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		remaining += int(value&127) * multiplier
		if value&128 == 0 {
			break
		}
		multiplier *= 128
	}
	payload := make([]byte, remaining)
	_, err = io.ReadFull(reader, payload)
	return header, payload, err
}

func TestPublishMQTTConnectsAndPublishesStatus(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	published := make(chan string, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		packetType, _, err := readMQTTPacket(reader)
		if err != nil || packetType != 0x10 {
			return
		}
		_, _ = connection.Write([]byte{0x20, 0x02, 0x00, 0x00})
		packetType, payload, err := readMQTTPacket(reader)
		if err != nil || packetType != 0x30 {
			return
		}
		published <- string(payload)
	}()
	address := listener.Addr().String()
	charge := 88.0
	if err := publishMQTT(MQTTConfig{Enabled: true, Broker: address, Topic: "fnos/ups"}, Status{TargetID: "ups-a", Charge: &charge}); err != nil {
		t.Fatal(err)
	}
	select {
	case payload := <-published:
		if !strings.Contains(payload, "fnos/ups/ups-a/status") || !strings.Contains(payload, `"charge":88`) {
			t.Fatalf("publish payload = %q", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("MQTT publish was not received")
	}
}
