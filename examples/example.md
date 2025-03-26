### Local docker

create config file
```yaml
#config.yaml
listenAddress: http://0.0.0.0:5137
appID: 123456
appSecret: 123456
privateKeyPath: /path/to/private.key

providers:
  docker-local:
    type: docker

trayTypes:
  cattery-tiny:
    provider: docker-local
    config:
      image: cattery-runner-tiny:latest
      namePrefix: cattery
```


build docker image
```bash
docker build -t cattery-runner-tiny:latest -f .\Dockerfile-cattery-tiny ..\src\
```

run cattery
```bash
cattery server
```

push GitHub webhook payload. Example payload can be found at `https://github.com/organizations/<org>/settings/hooks/<hook_id>`

example output:
```bash
time="2025-03-26T22:10:02+04:00" level=info msg="Starting webhook server on 0.0.0.0:5137"
time="2025-03-26T22:10:04+04:00" level=debug msg="Create tray 14081360499" name=trayProviderFactory
time="2025-03-26T22:10:04+04:00" level=debug msg="Agent registration request,  cattery-14081360499" agentId=cattery-14081360499 hostname=cattery-14081360499
time="2025-03-26T22:10:06+04:00" level=info msg="Agent cattery-14081360499, cattery-14081360499 registered with runner ID 17251" agentId=cattery-14081360499 hostname=cattery-14081360499
```
