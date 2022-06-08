package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/shlex"
	"github.com/gravitational/trace"
)

const tshAliasEnvKey = "TSH_ALIAS"
const tshEnvKey = "TSH"

// tryRunAlias inspects the arguments to see if the alias command should be run, and does so if required.
func tryRunAlias(aliases map[string]string, tshExecutable string, args []string, getEnv func(key string) string) (bool, error) {
	// find the alias to use
	aliasCmd, aliasIx := findCommand(args)

	// ignore aliases found in TSH_ALIAS list
	aliasesSeen := getSeenAliases(getEnv)
	for _, usedAlias := range aliasesSeen {
		if usedAlias == aliasCmd {
			return false, nil
		}
	}

	// match?
	aliasDef, ok := aliases[aliasCmd]
	if !ok {
		return false, nil
	}

	// parse alias, substitute arguments
	executable, definedArgs, err := parseAliasDefinition(aliasDef)
	if err != nil {
		return true, trace.Wrap(err)
	}

	runtimeArgs := args[aliasIx+1:]
	aliasArgs, err := replaceSpecialArgs(runtimeArgs, definedArgs)
	if err != nil {
		return true, trace.Wrap(err)
	}

	aliasesSeen = append(aliasesSeen, aliasCmd)

	return true, runAliasCommand(tshExecutable, aliasesSeen, executable, aliasArgs)
}

// runAliasCommand actually runs requested alias command.
func runAliasCommand(tshExecutable string, aliasesSeen []string, executable string, arguments []string) error {
	spew.Sprintln(tshExecutable, aliasesSeen, executable, arguments)

	// special treatment: expand $TSH variable.
	// Note: this allows for aliases as seen in RFD, with definition like "$TSH login --auth=...".
	if executable == "$"+tshEnvKey {
		executable = tshExecutable
	}

	execPath, err := exec.LookPath(executable)
	if err != nil {
		return trace.Wrap(err, "failed to find a executable %q", executable)
	}

	cmd := exec.Command(execPath, arguments...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// preserve existing env
	cmd.Env = os.Environ()

	env := map[string]string{
		tshEnvKey:      tshExecutable,
		tshAliasEnvKey: strings.Join(aliasesSeen, ","),
	}

	// add new entries
	for key, value := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%v=%v", key, value))
	}

	//
	log.Debugf("running command: %v", cmd)

	err = cmd.Run()
	if err == nil {
		return nil
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		return trace.Wrap(exitErr)
	}

	return trace.Wrap(err, "failed to run command: %v %v", execPath, strings.Join(arguments, " "))
}

// findCommand inspects the argument list to find first non-option (i.e. command), returning it along with the index it was found at.
func findCommand(args []string) (string, int) {
	aliasCmd := ""
	aliasIx := -1

	for i, arg := range args {
		if arg == "" {
			continue
		}

		if strings.HasPrefix(arg, "-") {
			continue
		}

		aliasCmd = arg
		aliasIx = i
		break
	}

	return aliasCmd, aliasIx
}

// getSeenAliases fetches TSH_ALIAS env variable and parses it, to produce the list of already executed aliases.
func getSeenAliases(getEnv func(key string) string) []string {
	var aliasesSeen []string

	for _, val := range strings.Split(getEnv(tshAliasEnvKey), ",") {
		if strings.TrimSpace(val) != "" {
			aliasesSeen = append(aliasesSeen, val)
		}
	}

	return aliasesSeen
}

// parseAliasDefinition parses the alias definition into individual components: executable to run and the arguments.
func parseAliasDefinition(aliasDef string) (string, []string, error) {
	elems, err := shlex.Split(aliasDef)
	if err != nil {
		return "", nil, trace.Wrap(err)
	}

	if len(elems) == 0 {
		return "", nil, trace.BadParameter("invalid alias definition, no executable provided")
	}

	executable := elems[0]
	params := elems[1:]
	return executable, params, nil
}

// replaceSpecialArgs replaces special parameters in aliasParms, such as $0, $1, $@, with actual given args.
func replaceSpecialArgs(runtimeArgs []string, definedArgs []string) ([]string, error) {
	var out []string

	for _, param := range definedArgs {
		if param == "$@" {
			out = append(out, runtimeArgs...)
			continue
		}

		// $0, $1...
		if matched, _ := regexp.MatchString("^[$]\\d+$", param); matched {
			argNum, err := strconv.Atoi(param[1:])
			if err != nil {
				return nil, trace.Wrap(err, "invalid alias: bad argument reference, cannot parse %q", param)
			}

			if argNum < 0 {
				return nil, trace.BadParameter("invalid alias: negative reference %q", param)
			}

			// copy shell behaviour: silently insert empty string.
			if argNum >= len(runtimeArgs) {
				out = append(out, "")
				continue
			}
			arg := runtimeArgs[argNum]
			out = append(out, arg)
			continue
		}

		// no special treatment
		out = append(out, param)
	}

	return out, nil
}
