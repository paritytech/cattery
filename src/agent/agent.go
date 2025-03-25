package agent

import (
	"cattery/lib/messages"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
)

var RunnerFolder string
var CatteryServerUrl string

func Start() {
	var registerResponse = registerAgent()

	var listenerPath = path.Join(RunnerFolder, "bin", "Runner.Listener")

	var command = exec.Command(listenerPath, "run", "--jitconfig", registerResponse.JitConfig)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	err := command.Run()
	if err != nil {
		log.Println(err)
	}

}

// createClient creates a new http client
func createClient() *http.Client {
	var client = &http.Client{}

	return client
}

// getJitConfig request just-in-time runner configuration from the Cattery server
// and returns the configuration as a base64 encoded string
//
// https://docs.github.com/en/rest/actions/self-hosted-runners?apiVersion=2022-11-28#create-configuration-for-a-just-in-time-runner-for-an-organization
func registerAgent() *messages.RegisterResponse {

	var client = createClient()

	var hostName, _ = os.Hostname()
	var request, _ = http.NewRequest("GET", CatteryServerUrl+"/agent/register/"+hostName, nil)
	response, _ := client.Do(request)

	if response.StatusCode == http.StatusOK {

		var registerResponse *messages.RegisterResponse = &messages.RegisterResponse{}
		err := json.NewDecoder(response.Body).Decode(registerResponse)
		if err != nil {
			log.Println(err)
		}

		return registerResponse

	} else {
		log.Println("Error")
	}

	return nil
}
