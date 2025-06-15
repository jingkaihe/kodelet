I'd like to extend the existing bash tool to support support a background parameter

it can be true/false

when the background=True, the command will be executed in the background. it is best suited for:
* running long running process, such as a guvicorn server, a web server or database process
* long running program (such as tests, or command that seemingly will take a long time)

when a Command with background=true is spin up, the tool output should be something like "process is up and running, output of the process can be viewed at ./kodelet/$pid/out.log"
the stdout and stderr of the process should be written into ./kodelet/$pid/out.log
the pid number, the command and the location to the log should be recorded into the state

when the background=True, the timeout parameter can be 0
