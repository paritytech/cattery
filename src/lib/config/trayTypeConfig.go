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
//
// RunnerFolder is the path inside the guest where the GitHub Actions runner
// distribution lives. The provider's default bootstrap passes it as the
// `--runner-folder` flag to `cattery agent` (which is required by the agent).
// Defaults to /cattery if empty. To take over the agent invocation entirely
// (e.g. when the image starts the agent itself via systemd), put your own
// `exec ...` at the end of Script — the default exec emitted afterwards
// becomes unreachable.
type NomadTrayConfig struct {
	TrayConfig
	JobId        string `yaml:"jobId"`
	Script       string `yaml:"script"`
	RunnerFolder string `yaml:"runnerFolder"`
}
