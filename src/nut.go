package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

type UPSInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Status struct {
	TargetID           string            `json:"target_id,omitempty"`
	TargetName         string            `json:"target_name,omitempty"`
	Connected          bool              `json:"connected"`
	TS                 int64             `json:"ts"`
	Error              string            `json:"error"`
	UPSName            string            `json:"ups_name,omitempty"`
	UPSList            []UPSInfo         `json:"ups_list"`
	Status             string            `json:"status,omitempty"`
	StatusFlags        []string          `json:"status_flags,omitempty"`
	StatusText         string            `json:"status_text,omitempty"`
	Charge             *float64          `json:"charge"`
	Load               *float64          `json:"load"`
	Runtime            *float64          `json:"runtime"`
	InputVoltage       *float64          `json:"input_voltage"`
	OutputVoltage      *float64          `json:"output_voltage"`
	BatteryVoltage     *float64          `json:"battery_voltage"`
	InputFrequency     *float64          `json:"input_frequency"`
	RealPower          *float64          `json:"real_power"`
	RealPowerEstimated bool              `json:"real_power_estimated"`
	Temperature        *float64          `json:"temperature"`
	InputTransferLow   *float64          `json:"input_transfer_low"`
	InputTransferHigh  *float64          `json:"input_transfer_high"`
	InputSensitivity   string            `json:"input_sensitivity,omitempty"`
	UPSModel           string            `json:"ups_model,omitempty"`
	UPSMfr             string            `json:"ups_mfr,omitempty"`
	UPSSerial          string            `json:"ups_serial,omitempty"`
	BatteryType        string            `json:"battery_type,omitempty"`
	Profile            DeviceProfile     `json:"profile"`
	Raw                map[string]string `json:"raw,omitempty"`
}

type DeviceProfile struct {
	Brand            string   `json:"brand,omitempty"`
	Series           string   `json:"series,omitempty"`
	Model            string   `json:"model,omitempty"`
	CapacityVA       int      `json:"capacity_va,omitempty"`
	RatedPowerW      int      `json:"rated_power_w,omitempty"`
	BatteryChemistry string   `json:"battery_chemistry,omitempty"`
	KnownQuirks      []string `json:"known_quirks,omitempty"`
}

type NUTCapabilities struct {
	Commands     []string `json:"commands"`
	WritableVars []string `json:"writable_vars"`
}

type NutClient struct {
	Host     string
	Port     int
	Timeout  time.Duration
	Username string
	Password string
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

func (n NutClient) authenticatedCommand(command string) (string, error) {
	connection, err := net.DialTimeout("tcp", net.JoinHostPort(n.Host, strconv.Itoa(n.Port)), n.Timeout)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(n.Timeout))
	reader := bufio.NewReader(connection)
	commands := []string{}
	if n.Username != "" {
		commands = append(commands, "USERNAME "+n.Username, "PASSWORD "+n.Password)
	}
	commands = append(commands, command)
	for _, item := range commands {
		if _, err := io.WriteString(connection, item+"\n"); err != nil {
			return "", err
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ERR ") {
			return "", errors.New(line)
		}
		if item == command {
			return line, nil
		}
	}
	return "", errors.New("NUT 未返回命令结果")
}

func (n NutClient) InstantCommand(upsName, command string) error {
	allowed := map[string]bool{"test.battery.start.quick": true, "test.battery.start.deep": true, "test.battery.stop": true}
	if !allowed[command] {
		return errors.New("不允许执行该 UPS 命令")
	}
	response, err := n.authenticatedCommand("INSTCMD " + upsName + " " + command)
	if err != nil {
		return err
	}
	if response != "OK" {
		return errors.New("NUT 命令未确认成功: " + response)
	}
	return nil
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

func (n NutClient) Capabilities(upsName string) (NUTCapabilities, error) {
	commands, err := n.Commands(upsName)
	if err != nil {
		return NUTCapabilities{}, err
	}
	writableVars, err := n.WritableVars(upsName)
	if err != nil {
		return NUTCapabilities{}, err
	}
	return NUTCapabilities{Commands: commands, WritableVars: writableVars}, nil
}

func (n NutClient) Commands(upsName string) ([]string, error) {
	commandsText, err := n.command("LIST CMD " + upsName)
	if err != nil {
		return nil, err
	}
	commands := []string{}
	for _, line := range strings.Split(commandsText, "\n") {
		fields := splitNut(line)
		if len(fields) >= 3 && fields[0] == "CMD" && fields[1] == upsName {
			commands = append(commands, fields[2])
		}
	}
	sort.Strings(commands)
	return commands, nil
}

func (n NutClient) WritableVars(upsName string) ([]string, error) {
	writableText, err := n.command("LIST RW " + upsName)
	if err != nil {
		return nil, err
	}
	writableVars := []string{}
	for _, line := range strings.Split(writableText, "\n") {
		fields := splitNut(line)
		if len(fields) >= 3 && fields[0] == "RW" && fields[1] == upsName {
			writableVars = append(writableVars, fields[2])
		}
	}
	sort.Strings(writableVars)
	return writableVars, nil
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
	raw := make(map[string]string, len(values))
	for key, value := range values {
		raw[key] = value
	}
	profile := identifyDevice(values)
	realPower, estimated := effectiveRealPower(values, profile)
	return Status{
		Connected: true, TS: time.Now().Unix(), UPSName: upsName, UPSList: upsList, Status: status,
		StatusFlags: flags, StatusText: statusText(flags), Charge: floatPointer(values["battery.charge"]),
		Load: floatPointer(values["ups.load"]), Runtime: floatPointer(values["battery.runtime"]),
		InputVoltage: floatPointer(values["input.voltage"]), OutputVoltage: floatPointer(values["output.voltage"]),
		BatteryVoltage: floatPointer(values["battery.voltage"]), InputFrequency: floatPointer(values["input.frequency"]),
		RealPower: realPower, RealPowerEstimated: estimated, Temperature: floatPointer(firstNonEmpty(values["ups.temperature"], values["battery.temperature"])),
		InputTransferLow: floatPointer(values["input.transfer.low"]), InputTransferHigh: floatPointer(values["input.transfer.high"]),
		InputSensitivity: values["input.sensitivity"],
		UPSModel:         firstNonEmpty(values["ups.model"], values["device.model"]),
		UPSMfr:           firstNonEmpty(values["ups.mfr"], values["device.mfr"]),
		UPSSerial:        firstNonEmpty(values["ups.serial"], values["device.serial"]), BatteryType: values["battery.type"], Profile: profile, Raw: raw,
	}
}

func statusText(flags []string) string {
	texts := make([]string, 0, len(flags))
	for _, flag := range flags {
		if label := statusLabels[flag]; label != "" {
			texts = append(texts, label)
		} else {
			texts = append(texts, flag)
		}
	}
	return strings.Join(texts, " / ")
}

func effectiveRealPower(values map[string]string, profile DeviceProfile) (*float64, bool) {
	if direct := floatPointer(values["ups.realpower"]); direct != nil {
		return direct, false
	}
	nominal := floatPointer(values["ups.realpower.nominal"])
	if nominal == nil && profile.RatedPowerW > 0 {
		value := float64(profile.RatedPowerW)
		nominal = &value
	}
	load := floatPointer(values["ups.load"])
	if nominal == nil || load == nil {
		return nil, false
	}
	value := *nominal * *load / 100
	return &value, true
}

func identifyDevice(values map[string]string) DeviceProfile {
	manufacturer := firstNonEmpty(values["ups.mfr"], values["device.mfr"])
	model := firstNonEmpty(values["ups.model"], values["device.model"])
	profile := DeviceProfile{Brand: manufacturer, Model: model}
	upperModel := strings.ToUpper(model)
	upperManufacturer := strings.ToUpper(manufacturer)
	if strings.Contains(upperModel, "BK650M2-CH") && (strings.Contains(upperManufacturer, "APC") || strings.Contains(upperManufacturer, "AMERICAN POWER CONVERSION")) {
		profile.Brand = "施耐德 APC"
		profile.Series = "Back-UPS"
		profile.CapacityVA = 650
		profile.RatedPowerW = 390
		profile.BatteryChemistry = "铅酸电池"
		profile.KnownQuirks = []string{"市电状态可能同时上报 OL DISCHRG", "部分 NUT/固件组合可能短暂误报 LB/RB", "电池生产日期 2001/01/01 通常是无效占位值"}
	}
	if nominal := floatPointer(values["ups.realpower.nominal"]); nominal != nil {
		profile.RatedPowerW = int(*nominal + 0.5)
	}
	if profile.BatteryChemistry == "" && strings.EqualFold(values["battery.type"], "PbAc") {
		profile.BatteryChemistry = "铅酸电池"
	}
	return profile
}

func firstNonEmpty(first, second string) string {
	if first != "" {
		return first
	}
	return second
}
