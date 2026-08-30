package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type UPSInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Status struct {
	Connected      bool              `json:"connected"`
	TS             int64             `json:"ts"`
	Error          string            `json:"error"`
	UPSName        string            `json:"ups_name,omitempty"`
	UPSList        []UPSInfo         `json:"ups_list"`
	Status         string            `json:"status,omitempty"`
	StatusFlags    []string          `json:"status_flags,omitempty"`
	StatusText     string            `json:"status_text,omitempty"`
	Charge         *float64          `json:"charge"`
	Load           *float64          `json:"load"`
	Runtime        *float64          `json:"runtime"`
	InputVoltage   *float64          `json:"input_voltage"`
	OutputVoltage  *float64          `json:"output_voltage"`
	BatteryVoltage *float64          `json:"battery_voltage"`
	InputFrequency *float64          `json:"input_frequency"`
	UPSModel       string            `json:"ups_model,omitempty"`
	UPSMfr         string            `json:"ups_mfr,omitempty"`
	UPSSerial      string            `json:"ups_serial,omitempty"`
	BatteryType    string            `json:"battery_type,omitempty"`
	Raw            map[string]string `json:"raw,omitempty"`
}

type NutClient struct {
	Host    string
	Port    int
	Timeout time.Duration
}

func (n NutClient) command(command string) (string, error) {
	connection, err := net.DialTimeout("tcp", net.JoinHostPort(n.Host, strconv.Itoa(n.Port)), n.Timeout)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(n.Timeout))
	if _, err = io.WriteString(connection, command+"\n"); err != nil {
		return "", err
	}
	var response bytes.Buffer
	reader := bufio.NewReader(connection)
	endMarker := "END " + command
	for response.Len() < 1024*1024 {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			response.WriteString(line)
			if strings.TrimSpace(line) == endMarker {
				break
			}
			if !strings.HasPrefix(command, "LIST ") {
				break
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return "", readErr
		}
	}
	text := response.String()
	if strings.HasPrefix(text, "ERR ") {
		return "", errors.New(strings.TrimSpace(text))
	}
	return text, nil
}

func splitNut(line string) []string {
	var fields []string
	var current strings.Builder
	quoted, escaped := false, false
	flush := func() {
		if current.Len() > 0 {
			fields = append(fields, current.String())
			current.Reset()
		}
	}
	for _, character := range strings.TrimSpace(line) {
		if escaped {
			current.WriteRune(character)
			escaped = false
			continue
		}
		if character == '\\' && quoted {
			escaped = true
			continue
		}
		if character == '"' {
			quoted = !quoted
			continue
		}
		if (character == ' ' || character == '\t') && !quoted {
			flush()
			continue
		}
		current.WriteRune(character)
	}
	flush()
	return fields
}

func (n NutClient) ListUPS() ([]UPSInfo, error) {
	text, err := n.command("LIST UPS")
	if err != nil {
		return nil, err
	}
	items := []UPSInfo{}
	for _, line := range strings.Split(text, "\n") {
		fields := splitNut(line)
		if len(fields) >= 2 && fields[0] == "UPS" {
			description := ""
			if len(fields) >= 3 {
				description = fields[2]
			}
			items = append(items, UPSInfo{Name: fields[1], Description: description})
		}
	}
	return items, nil
}

func (n NutClient) Vars(upsName string) (map[string]string, error) {
	text, err := n.command("LIST VAR " + upsName)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		fields := splitNut(line)
		if len(fields) >= 4 && fields[0] == "VAR" && fields[1] == upsName {
			values[fields[2]] = fields[3]
		}
	}
	return values, nil
}

func floatPointer(value string) *float64 {
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

var statusLabels = map[string]string{
	"OL": "市电供电", "OB": "电池供电", "LB": "电池低电量", "CHRG": "充电中",
	"DISCHRG": "放电中", "OVER": "过载", "RB": "需要更换电池", "BYPASS": "旁路",
	"CAL": "校准中", "OFF": "已关闭",
}

func normalize(upsName string, upsList []UPSInfo, values map[string]string) Status {
	status := values["ups.status"]
	flags := strings.Fields(status)
	texts := make([]string, 0, len(flags))
	for _, flag := range flags {
		if label := statusLabels[flag]; label != "" {
			texts = append(texts, label)
		} else {
			texts = append(texts, flag)
		}
	}
	raw := map[string]string{}
	for _, key := range []string{"battery.charge.low", "battery.runtime.low", "ups.realpower.nominal", "ups.power.nominal", "input.voltage.nominal", "output.voltage.nominal"} {
		if values[key] != "" {
			raw[key] = values[key]
		}
	}
	return Status{
		Connected: true, TS: time.Now().Unix(), UPSName: upsName, UPSList: upsList, Status: status,
		StatusFlags: flags, StatusText: strings.Join(texts, " / "), Charge: floatPointer(values["battery.charge"]),
		Load: floatPointer(values["ups.load"]), Runtime: floatPointer(values["battery.runtime"]),
		InputVoltage: floatPointer(values["input.voltage"]), OutputVoltage: floatPointer(values["output.voltage"]),
		BatteryVoltage: floatPointer(values["battery.voltage"]), InputFrequency: floatPointer(values["input.frequency"]),
		UPSModel:  firstNonEmpty(values["ups.model"], values["device.model"]),
		UPSMfr:    firstNonEmpty(values["ups.mfr"], values["device.mfr"]),
		UPSSerial: firstNonEmpty(values["ups.serial"], values["device.serial"]), BatteryType: values["battery.type"], Raw: raw,
	}
}

func firstNonEmpty(first, second string) string {
	if first != "" {
		return first
	}
	return second
}
