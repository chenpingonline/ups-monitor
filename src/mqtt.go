package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

func mqttString(value string) ([]byte, error) {
	if len(value) > 65535 {
		return nil, errors.New("MQTT 字符串过长")
	}
	buffer := make([]byte, 2+len(value))
	binary.BigEndian.PutUint16(buffer[:2], uint16(len(value)))
	copy(buffer[2:], value)
	return buffer, nil
}

func mqttPacket(packetType byte, payload []byte) []byte {
	packet := []byte{packetType}
	remaining := len(payload)
	for {
		encoded := byte(remaining % 128)
		remaining /= 128
		if remaining > 0 {
			encoded |= 128
		}
		packet = append(packet, encoded)
		if remaining == 0 {
			break
		}
	}
	return append(packet, payload...)
}

func publishMQTT(config MQTTConfig, status Status) error {
	if !config.Enabled {
		return nil
	}
	address := config.Broker
	if _, _, err := net.SplitHostPort(address); err != nil {
		host := strings.TrimPrefix(strings.TrimSuffix(address, "]"), "[")
		address = net.JoinHostPort(host, "1883")
	}
	connection, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(4 * time.Second))
	clientID := config.ClientID
	if clientID == "" {
		clientID = "fnos-ups-monitor"
	}
	protocol, _ := mqttString("MQTT")
	client, err := mqttString(clientID)
	if err != nil {
		return err
	}
	flags := byte(0x02)
	payload := client
	if config.Username != "" {
		flags |= 0x80
		username, err := mqttString(config.Username)
		if err != nil {
			return err
		}
		payload = append(payload, username...)
		if config.Password != "" {
			flags |= 0x40
			password, err := mqttString(config.Password)
			if err != nil {
				return err
			}
			payload = append(payload, password...)
		}
	}
	variable := append(protocol, 0x04, flags, 0x00, 0x1e)
	if _, err := connection.Write(mqttPacket(0x10, append(variable, payload...))); err != nil {
		return err
	}
	ack := make([]byte, 4)
	if _, err := io.ReadFull(bufio.NewReader(connection), ack); err != nil {
		return err
	}
	if ack[0] != 0x20 || ack[1] != 0x02 || ack[3] != 0x00 {
		return fmt.Errorf("MQTT CONNECT 被拒绝，返回码 %d", ack[3])
	}
	body, err := json.Marshal(status)
	if err != nil {
		return err
	}
	topic, err := mqttString(config.Topic + "/" + status.TargetID + "/status")
	if err != nil {
		return err
	}
	if _, err := connection.Write(mqttPacket(0x30, append(topic, body...))); err != nil {
		return err
	}
	_, _ = connection.Write([]byte{0xe0, 0x00})
	return nil
}
