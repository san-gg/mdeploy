## MDeploy
MDeploy is a command-line tool for remote server deployment and management over SSH. It simplifies common tasks such as executing commands, transferring files, and running deployment scripts on remote servers.

Features
-  **Deploy** applications using YAML configuration files
-  **Execute** commands remotely over SSH
-  **Run** scripts on remote servers with arguments
-  **Copy** files between local and remote servers
-  Progress monitoring with visual indicators

## Installation
Prerequisites
-  Go 1.24 or higher
```bash
# Clone the repository
git clone https://github.com/san-gg/mdeploy.git
cd mdeploy

# Build the application
go build -o mdeploy

# Make it available in your PATH (optional)
sudo mv mdeploy /usr/local/bin/
```
## Usage
**Basic Commands**
```bash
mdeploy [command] [flags]
```
-  [deploy](cmd/deploy/deploy.go) - Deploy using configuration from YAML files
-  [exec](cmd/ssh/exec/exec.go) - Execute commands on remote servers
-  [run](cmd/ssh/run/run.go) - Execute scripts on remote servers with arguments
-  [copy](cmd/ssh/copy/copy.go) - Copy files between local and remote servers

**Global Flags**
-  ```-T, --trust``` - Trust SSH server host key
-  ```--plain``` - Print plain output without progress bars
-  ```-h, --help``` - Help for mdeploy
-  ```-v, --version``` - Version for mdeploy

**Command Details**

Deploy applications using YAML configuration files:
```bash
mdeploy deploy mydeployment.yml
```
YAML Configuration Format
```yml
name: "My Deployment"
credential:
  source: "${SERVER_HOST}"
  username: "${SSH_USER}"
  password: "${SSH_PASSWORD}"
steps:
  - task: COPYTOSERVER
    source: "local/file.txt"
    destination: "/remote/path/"
    description: "Copying configuration files"
  
  - task: EXEC
    command: "chmod +x /remote/path/script.sh"
    description: "Set execute permissions"
  
  - task: RUN
    file: "/remote/path/script.sh"
    description: "Running deployment script"
  
  - task: COPYFROMSERVER
    source: "/remote/path/logs.txt"
    destination: "local/logs/"
    description: "Retrieving logs"
```

**Exec Command**

Execute commands on remote servers:
```bash
mdeploy exec --host=server.example.com --user=admin --password=password123 "ls -la"
```
**Run Command**

Execute scripts on remote servers:
```bash
mdeploy run --host=server.example.com --user=admin --password=password123 local/script.sh arg1 arg2
```

**Copy Command**

Copy files between local and remote servers:
```bash
# Copy from local to remote
mdeploy copy local/file.txt user:password@server.example.com:/path/

# Copy from remote to local
mdeploy copy user:password@server.example.com:/path/file.txt local/path/
```

**Environment Variables**

MDeploy supports loading environment variables from a .env file in the current directory, which can be used to store sensitive information such as server credentials.

Example .env file:
```
SERVER_HOST=server.example.com
SSH_USER=admin
SSH_PASSWORD=password123
```

## License
[MIT License](LICENSE)
