package agents

type Agent struct {
	AgentId  string `json:"agentId"`
	RunnerId int64  `json:"runnerId"`
	Shutdown bool   `json:"shutdown"`
}
