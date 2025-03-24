package agent

import "net/http"

type CatteryClient struct {
	httpClient *http.Client
}
