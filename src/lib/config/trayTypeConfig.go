package config

type TrayConfig interface {
}

type GoogleTrayConfig struct {
	TrayConfig
	Project          string   `yaml:"project"`
	Zones            []string `yaml:"zones"`
	MachineType      string   `yaml:"machineType"`
	InstanceTemplate string   `yaml:"instanceTemplate"`
	NamePrefix       string   `yaml:"namePrefix"`
}

type DockerTrayConfig struct {
	TrayConfig
	Image      string `yaml:"image"`
	NamePrefix string `yaml:"namePrefix"`
}

// NomadTrayConfig configures a Nomad-dispatched tray.
//
// JobId is the ID of a parameterized parent job already registered in Nomad.
// Resources, driver and constraints come from that job spec — Nomad does not
// allow overriding them at dispatch time. Use distinct parameterized jobs for
// distinct resource shapes.
//
// Script is an optional inline bash snippet inlined into the dispatched
// payload before the agent is exec'd. Use it for per-tray-type setup
// (mounting volumes, installing tools, etc.). Use YAML's `|` block scalar to
// embed multi-line scripts.
type NomadTrayConfig struct {
	TrayConfig
	JobId  string `yaml:"jobId" validate:"required"`
	Script string `yaml:"script"`
}
