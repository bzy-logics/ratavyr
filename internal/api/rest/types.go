// Package rest holds the REST JSON types of docs/rest-api.md, their
// conversion from the engine's protobuf messages, error mapping and small
// HTTP helpers shared by the handler packages.
//
// The JSON shape is defined explicitly here (not via protojson): enums are
// lowercase strings without their prefix and unset optional values are null.
package rest

import (
	"strings"

	enginev1 "github.com/bzy-logics/ratavyr/gen/motioncore/engine/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// EngineInfo is the engine handshake info.
type EngineInfo struct {
	APIVersion            string `json:"api_version"`
	EngineVersion         string `json:"engine_version"`
	MCUProtocolVersion    uint32 `json:"mcu_protocol_version"`
	PluginAPIVersion      string `json:"plugin_api_version"`
	FaultInjectionEnabled bool   `json:"fault_injection_enabled"`
}

// Telemetry is a robot status sample.
type Telemetry struct {
	Seq    uint64          `json:"seq"`
	TimeMS uint64          `json:"time_ms"`
	State  string          `json:"state"`
	Fault  *Fault          `json:"fault"`
	MCU    MCUTelemetry    `json:"mcu"`
	Axes   []AxisTelemetry `json:"axes"`
	// Tool is the tool pose of a serial-chain robot (engine-api >= 0.3);
	// null otherwise, and then move_l is not available.
	Tool *ToolPose `json:"tool"`
}

// ToolPose is the tool point in the robot base frame.
type ToolPose struct {
	PositionMM [3]float64 `json:"position_mm"`
	// RotationDeg is roll, pitch, yaw: R = Rz(yaw)·Ry(pitch)·Rx(roll).
	RotationDeg [3]float64 `json:"rotation_deg"`
}

// Fault is a latched fault.
type Fault struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Axis    *string `json:"axis"`
}

// MCUTelemetry is the MCU link status.
type MCUTelemetry struct {
	Connected          bool     `json:"connected"`
	Transport          string   `json:"transport"`
	Target             string   `json:"target"`
	Board              string   `json:"board"`
	FirmwareVersion    string   `json:"firmware_version"`
	ProtocolVersion    uint32   `json:"protocol_version"`
	BuildHash          string   `json:"build_hash"`
	Capabilities       []string `json:"capabilities"`
	QueueCapacity      uint32   `json:"queue_capacity"`
	QueueDepth         uint32   `json:"queue_depth"`
	ClockOffsetUS      int64    `json:"clock_offset_us"`
	RTTUS              uint32   `json:"rtt_us"`
	TxFrames           uint64   `json:"tx_frames"`
	RxFrames           uint64   `json:"rx_frames"`
	CRCErrors          uint64   `json:"crc_errors"`
	LastHeartbeatMSAgo *uint64  `json:"last_heartbeat_ms_ago"`
}

// AxisTelemetry is the per-axis status.
type AxisTelemetry struct {
	ID                string  `json:"id"`
	Homed             bool    `json:"homed"`
	Enabled           bool    `json:"enabled"`
	CommandedPosition float64 `json:"commanded_position"`
	MeasuredPosition  float64 `json:"measured_position"`
	Velocity          float64 `json:"velocity"`
	Endstop           bool    `json:"endstop"`
	// Homing is true while the axis is homing (engine-api >= 0.1.4).
	Homing bool `json:"homing"`
}

// RobotConfig is the loaded robot configuration.
type RobotConfig struct {
	Name    string       `json:"name"`
	Profile string       `json:"profile"`
	MCUs    []MCUConfig  `json:"mcus"`
	Axes    []AxisConfig `json:"axes"`
	// HomeAll is how the home_all command proceeds; null with engines older
	// than API 0.1.4.
	HomeAll *HomeAllPlan `json:"home_all"`
	// CollisionChecking is true when the engine refuses moves that would make
	// the robot collide with itself or the floor (engine-api >= 0.1.4).
	CollisionChecking bool `json:"collision_checking"`
}

// HomeAllPlan lists the groups of the home_all command: the axes of a group
// home at the same time, groups in order.
type HomeAllPlan struct {
	Groups      [][]string `json:"groups"`
	StartPose   *string    `json:"start_pose"`
	FromProfile bool       `json:"from_profile"`
}

// MCUConfig describes one configured MCU.
type MCUConfig struct {
	Name       string `json:"name"`
	Board      string `json:"board"`
	Transport  string `json:"transport"`
	WatchdogMS uint32 `json:"watchdog_ms"`
}

// AxisConfig describes one configured axis.
type AxisConfig struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	Unit            string   `json:"unit"`
	Min             float64  `json:"min"`
	Max             float64  `json:"max"`
	MaxVelocity     float64  `json:"max_velocity"`
	MaxAcceleration float64  `json:"max_acceleration"`
	HomePosition    *float64 `json:"home_position"`
	StepsPerUnit    float64  `json:"steps_per_unit"`
	MCU             string   `json:"mcu"`
	MCUChannel      uint32   `json:"mcu_channel"`
	Actuator        string   `json:"actuator"`
	StepPin         string   `json:"step_pin"`
	DirPin          string   `json:"dir_pin"`
	EnablePin       string   `json:"enable_pin"`
	EndstopPin      *string  `json:"endstop_pin"`
	// Geometry is the joint's kinematic placement; null when the robot
	// profile defines none (engine-api >= 0.1.3).
	Geometry *AxisGeometry `json:"geometry"`
}

// AxisGeometry is a URDF-style joint placement:
// frame(child) = frame(parent) · Translate(origin_mm) · RPY(rotation_deg) · Joint(q about axis).
type AxisGeometry struct {
	// Parent axis id; null = mounted on the robot base.
	Parent      *string    `json:"parent"`
	OriginMM    [3]float64 `json:"origin_mm"`
	RotationDeg [3]float64 `json:"rotation_deg"`
	Axis        [3]float64 `json:"axis"`
	// TipMM is the end of this joint's link (e.g. the tool flange) in its
	// joint frame; null when not configured.
	TipMM *[3]float64 `json:"tip_mm"`
	// RadiusMM is the link radius of the collision model; null if not set.
	RadiusMM *float64 `json:"radius_mm"`
}

// Plugin is a loaded plugin.
type Plugin struct {
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	APIVersion  string   `json:"api_version"`
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	Provides    []string `json:"provides"`
	Requires    []string `json:"requires"`
	Permissions []string `json:"permissions"`
	State       string   `json:"state"`
	Error       *string  `json:"error"`
}

// LogEntry is an engine log event.
type LogEntry struct {
	TimeMS  uint64 `json:"time_ms"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// FirmwareTarget is a supported firmware board.
type FirmwareTarget struct {
	Board          string `json:"board"`
	Description    string `json:"description"`
	FirmwareTarget string `json:"firmware_target"`
	RustTarget     string `json:"rust_target"`
	StepChannels   uint32 `json:"step_channels"`
	MaxStepRate    uint32 `json:"max_step_rate"`
	Provisional    bool   `json:"provisional"`
	IsVirtual      bool   `json:"is_virtual"`
}

// FirmwareJob is a firmware build or flash job.
type FirmwareJob struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	State        string            `json:"state"`
	MCU          string            `json:"mcu"`
	CreatedUnix  uint64            `json:"created_unix"`
	FinishedUnix *uint64           `json:"finished_unix"`
	Message      string            `json:"message"`
	Log          string            `json:"log"`
	Artifact     *FirmwareArtifact `json:"artifact"`
}

// FirmwareArtifact describes a built firmware image.
type FirmwareArtifact struct {
	Board                string   `json:"board"`
	FirmwareVersion      string   `json:"firmware_version"`
	BuildHash            string   `json:"build_hash"`
	ImageSHA256          string   `json:"image_sha256"`
	Toolchain            string   `json:"toolchain"`
	RequiredCapabilities []string `json:"required_capabilities"`
}

// ---------------------------------------------------------------------------
// Conversion
// ---------------------------------------------------------------------------

// Enum returns the REST string for a proto enum value: the value name
// lowercased without the enum's prefix (ROBOT_STATE_EMERGENCY_STOPPED ->
// "emergency_stopped"). Values unknown to this build (added by a newer engine
// minor version) map to "unknown".
func Enum(e protoreflect.Enum) string {
	v := e.Descriptor().Values().ByNumber(e.Number())
	if v == nil {
		return "unknown"
	}
	prefix := toUpperSnake(string(e.Descriptor().Name())) + "_"
	return strings.ToLower(strings.TrimPrefix(string(v.Name()), prefix))
}

// toUpperSnake converts CamelCase to UPPER_SNAKE (RobotState -> ROBOT_STATE).
func toUpperSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String())
}

func strs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// FromEngineInfo converts EngineInfo; nil stays nil.
func FromEngineInfo(i *enginev1.EngineInfo) *EngineInfo {
	if i == nil {
		return nil
	}
	return &EngineInfo{
		APIVersion:            i.GetApiVersion(),
		EngineVersion:         i.GetEngineVersion(),
		MCUProtocolVersion:    i.GetMcuProtocolVersion(),
		PluginAPIVersion:      i.GetPluginApiVersion(),
		FaultInjectionEnabled: i.GetFaultInjectionEnabled(),
	}
}

// FromTelemetry converts Telemetry.
func FromTelemetry(t *enginev1.Telemetry) Telemetry {
	out := Telemetry{
		Seq:    t.GetSeq(),
		TimeMS: t.GetTimeMs(),
		State:  Enum(t.GetState()),
		Axes:   make([]AxisTelemetry, 0, len(t.GetAxes())),
	}
	if f := t.GetFault(); f != nil {
		out.Fault = &Fault{Code: Enum(f.GetCode()), Message: f.GetMessage(), Axis: nonEmpty(f.GetAxis())}
	}
	m := t.GetMcu()
	out.MCU = MCUTelemetry{
		Connected:       m.GetConnected(),
		Transport:       m.GetTransport(),
		Target:          m.GetTarget(),
		Board:           m.GetBoard(),
		FirmwareVersion: m.GetFirmwareVersion(),
		ProtocolVersion: m.GetProtocolVersion(),
		BuildHash:       m.GetBuildHash(),
		Capabilities:    strs(m.GetCapabilities()),
		QueueCapacity:   m.GetQueueCapacity(),
		QueueDepth:      m.GetQueueDepth(),
		ClockOffsetUS:   m.GetClockOffsetUs(),
		RTTUS:           m.GetRttUs(),
		TxFrames:        m.GetTxFrames(),
		RxFrames:        m.GetRxFrames(),
		CRCErrors:       m.GetCrcErrors(),
	}
	if m != nil && m.LastHeartbeatMsAgo != nil {
		v := *m.LastHeartbeatMsAgo
		out.MCU.LastHeartbeatMSAgo = &v
	}
	for _, a := range t.GetAxes() {
		out.Axes = append(out.Axes, AxisTelemetry{
			ID:                a.GetId(),
			Homed:             a.GetHomed(),
			Enabled:           a.GetEnabled(),
			CommandedPosition: a.GetCommandedPosition(),
			MeasuredPosition:  a.GetMeasuredPosition(),
			Velocity:          a.GetVelocity(),
			Endstop:           a.GetEndstop(),
			Homing:            a.GetHoming(),
		})
	}
	if tp := t.GetTool(); tp != nil {
		v3 := func(v *enginev1.Vec3) [3]float64 { return [3]float64{v.GetX(), v.GetY(), v.GetZ()} }
		out.Tool = &ToolPose{PositionMM: v3(tp.GetPositionMm()), RotationDeg: v3(tp.GetRotationDeg())}
	}
	return out
}

// FromRobotConfig converts RobotConfig.
func FromRobotConfig(c *enginev1.RobotConfig) RobotConfig {
	out := RobotConfig{
		Name:    c.GetName(),
		Profile: c.GetProfile(),
		MCUs:    make([]MCUConfig, 0, len(c.GetMcus())),
		Axes:    make([]AxisConfig, 0, len(c.GetAxes())),
	}
	for _, m := range c.GetMcus() {
		out.MCUs = append(out.MCUs, MCUConfig{
			Name:       m.GetName(),
			Board:      m.GetBoard(),
			Transport:  m.GetTransport(),
			WatchdogMS: m.GetWatchdogMs(),
		})
	}
	for _, a := range c.GetAxes() {
		ac := AxisConfig{
			ID:              a.GetId(),
			Name:            a.GetName(),
			Kind:            Enum(a.GetKind()),
			Unit:            a.GetUnit(),
			Min:             a.GetMin(),
			Max:             a.GetMax(),
			MaxVelocity:     a.GetMaxVelocity(),
			MaxAcceleration: a.GetMaxAcceleration(),
			StepsPerUnit:    a.GetStepsPerUnit(),
			MCU:             a.GetMcu(),
			MCUChannel:      a.GetMcuChannel(),
			Actuator:        a.GetActuator(),
			StepPin:         a.GetStepPin(),
			DirPin:          a.GetDirPin(),
			EnablePin:       a.GetEnablePin(),
			EndstopPin:      nonEmpty(a.GetEndstopPin()),
		}
		if a.HomePosition != nil {
			v := *a.HomePosition
			ac.HomePosition = &v
		}
		if g := a.GetGeometry(); g != nil {
			v3 := func(v *enginev1.Vec3) [3]float64 { return [3]float64{v.GetX(), v.GetY(), v.GetZ()} }
			ac.Geometry = &AxisGeometry{
				Parent:      nonEmpty(g.GetParent()),
				OriginMM:    v3(g.GetOriginMm()),
				RotationDeg: v3(g.GetRotationDeg()),
				Axis:        v3(g.GetAxis()),
			}
			if t := g.GetTipMm(); t != nil {
				tip := v3(t)
				ac.Geometry.TipMM = &tip
			}
			if g.RadiusMm != nil {
				r := g.GetRadiusMm()
				ac.Geometry.RadiusMM = &r
			}
		}
		out.Axes = append(out.Axes, ac)
	}
	if p := c.GetHomeAll(); p != nil {
		plan := &HomeAllPlan{Groups: make([][]string, 0, len(p.GetGroups())), StartPose: nonEmpty(p.GetStartPose()), FromProfile: p.GetFromProfile()}
		for _, g := range p.GetGroups() {
			plan.Groups = append(plan.Groups, append([]string{}, g.GetAxes()...))
		}
		out.HomeAll = plan
	}
	out.CollisionChecking = c.GetCollisionChecking()
	return out
}

// FromPlugins converts a plugin list; never nil.
func FromPlugins(ps []*enginev1.Plugin) []Plugin {
	out := make([]Plugin, 0, len(ps))
	for _, p := range ps {
		out = append(out, Plugin{
			ID:          p.GetId(),
			Version:     p.GetVersion(),
			APIVersion:  p.GetApiVersion(),
			Kind:        p.GetKind(),
			Description: p.GetDescription(),
			Provides:    strs(p.GetProvides()),
			Requires:    strs(p.GetRequires()),
			Permissions: strs(p.GetPermissions()),
			State:       Enum(p.GetState()),
			Error:       nonEmpty(p.GetError()),
		})
	}
	return out
}

// FromLogEntry converts LogEntry.
func FromLogEntry(l *enginev1.LogEntry) LogEntry {
	return LogEntry{TimeMS: l.GetTimeMs(), Level: Enum(l.GetLevel()), Message: l.GetMessage()}
}

// FromFirmwareTargets converts a target list; never nil.
func FromFirmwareTargets(ts []*enginev1.FirmwareTarget) []FirmwareTarget {
	out := make([]FirmwareTarget, 0, len(ts))
	for _, t := range ts {
		out = append(out, FirmwareTarget{
			Board:          t.GetBoard(),
			Description:    t.GetDescription(),
			FirmwareTarget: t.GetFirmwareTarget(),
			RustTarget:     t.GetRustTarget(),
			StepChannels:   t.GetStepChannels(),
			MaxStepRate:    t.GetMaxStepRate(),
			Provisional:    t.GetProvisional(),
			IsVirtual:      t.GetIsVirtual(),
		})
	}
	return out
}

// FromFirmwareJob converts FirmwareJob.
func FromFirmwareJob(j *enginev1.FirmwareJob) FirmwareJob {
	out := FirmwareJob{
		ID:          j.GetId(),
		Kind:        Enum(j.GetKind()),
		State:       Enum(j.GetState()),
		MCU:         j.GetMcu(),
		CreatedUnix: j.GetCreatedUnix(),
		Message:     j.GetMessage(),
		Log:         j.GetLog(),
	}
	if f := j.GetFinishedUnix(); f != 0 {
		out.FinishedUnix = &f
	}
	if a := j.GetArtifact(); a != nil {
		out.Artifact = &FirmwareArtifact{
			Board:                a.GetBoard(),
			FirmwareVersion:      a.GetFirmwareVersion(),
			BuildHash:            a.GetBuildHash(),
			ImageSHA256:          a.GetImageSha256(),
			Toolchain:            a.GetToolchain(),
			RequiredCapabilities: strs(a.GetRequiredCapabilities()),
		}
	}
	return out
}
