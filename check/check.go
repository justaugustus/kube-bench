// Copyright © 2017 Aqua Security Software Ltd. <info@aquasec.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package check

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/golang/glog"
)

// NodeType indicates the type of node (master, node, federated).
type NodeType string

// State is the state of a control check.
type State string

const (
	// PASS check passed.
	PASS State = "PASS"
	// FAIL check failed.
	FAIL = "FAIL"
	// WARN could not carry out check.
	WARN = "WARN"
	// INFO informational message
	INFO = "INFO"

	// MASTER a master node
	MASTER NodeType = "master"
	// NODE a node
	NODE NodeType = "node"
	// FEDERATED a federated deployment.
	FEDERATED NodeType = "federated"
)

func handleError(err error, context string) (errmsg string) {
	if err != nil {
		errmsg = fmt.Sprintf("%s, error: %s\n", context, err)
	}
	return
}

// Check contains information about a recommendation in the
// CIS Kubernetes 1.6+ document.
type Check struct {
	ID          string `yaml:"id" json:"id"`
	Text        string
	Audit       string      `json:"omit"`
	Type        string      `json:"type"`
	Commands    []*exec.Cmd `json:"omit"`
	Tests       *tests      `json:"omit"`
	Set         bool        `json:"omit"`
	Remediation string
	State
}

// Run executes the audit commands specified in a check and outputs
// the results.
func (c *Check) Run() {
	// If check type is manual, force result to WARN.
	if c.Type == "manual" {
		c.State = WARN
		return
	}

	var out bytes.Buffer
	var errmsgs string

	// Check if command exists or exit with WARN.
	for _, cmd := range c.Commands {
		if !isShellCommand(cmd.Path) {
			c.State = WARN
			return
		}
	}

	// Run commands.
	n := len(c.Commands)
	if n == 0 {
		// Likely a warning message.
		c.State = WARN
		return
	}

	// Each command runs,
	//   cmd0 out -> cmd1 in, cmd1 out -> cmd2 in ... cmdn out -> os.stdout
	//   cmd0 err should terminate chain
	cs := c.Commands

	// Initialize command pipeline
	cs[n-1].Stdout = &out
	i := 1

	var err error
	errmsgs = ""

	for i < n {
		cs[i-1].Stdout, err = cs[i].StdinPipe()
		errmsgs += handleError(
			err,
			fmt.Sprintf("failed to run: %s\nfailed command: %s",
				c.Audit,
				cs[i].Args,
			),
		)
		i++
	}

	// Start command pipeline
	i = 0
	for i < n {
		err := cs[i].Start()
		errmsgs += handleError(
			err,
			fmt.Sprintf("failed to run: %s\nfailed command: %s",
				c.Audit,
				cs[i].Args,
			),
		)
		i++
	}

	// Complete command pipeline
	i = 0
	for i < n {
		err := cs[i].Wait()
		errmsgs += handleError(
			err,
			fmt.Sprintf("failed to run: %s\nfailed command:%s",
				c.Audit,
				cs[i].Args,
			),
		)

		if i < n-1 {
			cs[i].Stdout.(io.Closer).Close()
		}

		i++
	}

	if errmsgs != "" {
		glog.V(2).Info(errmsgs)
	}

	res := c.Tests.execute(out.String())
	if res {
		c.State = PASS
	} else {
		c.State = FAIL
	}
}

// textToCommand transforms an input text representation of commands to be
// run into a slice of commands.
// TODO: Make this more robust.
func textToCommand(s string) []*exec.Cmd {
	cmds := []*exec.Cmd{}

	cp := strings.Split(s, "|")

	for _, v := range cp {
		v = strings.Trim(v, " ")

		// TODO:
		// GOAL: To split input text into arguments for exec.Cmd.
		//
		// CHALLENGE: The input text may contain quoted strings that
		// must be passed as a unit to exec.Cmd.
		// eg. bash -c 'foo bar'
		// 'foo bar' must be passed as unit to exec.Cmd if not the command
		// will fail when it is executed.
		// eg. exec.Cmd("bash", "-c", "foo bar")
		//
		// PROBLEM: Current solution assumes the grouped string will always
		// be at the end of the input text.
		re := regexp.MustCompile(`^(.*)(['"].*['"])$`)
		grps := re.FindStringSubmatch(v)

		var cs []string
		if len(grps) > 0 {
			s := strings.Trim(grps[1], " ")
			cs = strings.Split(s, " ")

			s1 := grps[len(grps)-1]
			s1 = strings.Trim(s1, "'\"")

			cs = append(cs, s1)
		} else {
			cs = strings.Split(v, " ")
		}

		cmd := exec.Command(cs[0], cs[1:]...)
		cmds = append(cmds, cmd)
	}

	return cmds
}

func isShellCommand(s string) bool {
	cmd := exec.Command("/bin/sh", "-c", "command -v "+s)

	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}

	if strings.Contains(string(out), s) {
		return true
	}
	return false
}
