package main

func successResult(out string) CommandResult {
	code := 0
	return CommandResult{ExitCode: &code, Stdout: out}
}

func failureResult(out string) CommandResult {
	code := 1
	return CommandResult{ExitCode: &code, Stderr: out}
}
