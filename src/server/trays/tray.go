package trays

type Tray struct {
	AgentId  string `json:"agentId"`
	RunnerId int64  `json:"runnerId"`
	Hostname string `json:"hostname"`
}
