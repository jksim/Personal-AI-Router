// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"nvpair-shared/accel"
)

// qaicHostReady reports whether this host can run the Qualcomm engine. Each
// field fails on its own, because each one needs a different fix.
type qaicHostReady struct {
	CardPresent  bool
	CardAnswered bool // every SoC answered its status query and has a qaic hwmon
	SoCCount     int  // SoCs that answered and have a qaic hwmon
	NodesOpen    bool // no SoC refused its device node to this user
	DockerUsable bool
	P2PAllowed   bool // ACS redirect is off between the SoCs, so multi-SoC QPCs can run
}

// ok reports whether the engine can start. P2PAllowed is not part of it: without
// P2P, 1-SoC QPCs still run.
func (r qaicHostReady) ok() bool {
	return r.CardPresent && r.CardAnswered && r.NodesOpen && r.DockerUsable
}

// qaicHostProblem is one failed check, written for the user. A blocking problem
// stops the engine from starting; a non-blocking one only limits which QPCs run.
type qaicHostProblem struct {
	Message  string
	Fix      string
	Blocking bool
	Check    string
}

// The checks a problem can come from. A pull downloads a QPC and needs no
// Docker, so it skips the Docker check.
const (
	qaicCheckCard   = "card"
	qaicCheckDocker = "docker"
	qaicCheckP2P    = "p2p"
)

const qaicHostSetupDoc = "See docs/qaic-host-setup.mdx."

// currentQAICHost runs the readiness check on this host. The caller passes the
// result of its own Docker check, or nil where Docker does not matter.
func currentQAICHost(dockerErr error) (qaicHostReady, []qaicHostProblem) {
	// Without /proc/cmdline no ACS parameter can be seen, so P2P is not allowed.
	cmdline, _ := os.ReadFile("/proc/cmdline")
	socs, inspectErr := accel.InspectQAIC()
	return checkQAICHost(socs, inspectErr, string(cmdline), dockerErr)
}

// checkQAICHost evaluates host readiness from the SoCs that accel.InspectQAIC
// found, the kernel command line, and the result of a Docker check.
func checkQAICHost(socs []accel.QAICSoCState, inspectErr error, cmdline string, dockerErr error) (qaicHostReady, []qaicHostProblem) {
	var ready qaicHostReady
	var problems []qaicHostProblem

	if inspectErr != nil {
		problems = append(problems, qaicHostProblem{
			Message:  fmt.Sprintf("Could not list the PCI devices: %v.", inspectErr),
			Fix:      "Check that /sys is mounted and readable.",
			Blocking: true,
			Check:    qaicCheckCard,
		})
	} else if len(socs) == 0 {
		problems = append(problems, qaicHostProblem{
			Message:  "No Qualcomm Cloud AI 100 card was found.",
			Fix:      "Install a card, or check that `lspci -d 17cb:` lists it.",
			Blocking: true,
			Check:    qaicCheckCard,
		})
	}
	ready.CardPresent = inspectErr == nil && len(socs) > 0

	var denied, silent, noHwmon []string
	for _, soc := range socs {
		switch {
		case errors.Is(soc.QueryError, fs.ErrPermission):
			denied = append(denied, "/dev/accel/"+soc.Node)
		case !soc.Answered:
			silent = append(silent, soc.PCIAddress)
		case !soc.HasHwmon:
			noHwmon = append(noHwmon, soc.PCIAddress)
		default:
			ready.SoCCount++
		}
	}
	ready.CardAnswered = ready.CardPresent && ready.SoCCount == len(socs)
	ready.NodesOpen = len(denied) == 0
	if len(denied) > 0 {
		problems = append(problems, qaicHostProblem{
			Message:  fmt.Sprintf("This user cannot open %s.", strings.Join(denied, ", ")),
			Fix:      "Install the udev rule that gives a group access to /dev/accel, and add this user to that group. " + qaicHostSetupDoc,
			Blocking: true,
			Check:    qaicCheckCard,
		})
	}
	if len(silent) > 0 {
		problems = append(problems, qaicHostProblem{
			Message:  fmt.Sprintf("The card did not answer from %s.", strings.Join(silent, ", ")),
			Fix:      "Make sure the card is powered on and all of its SoCs have started, then try again.",
			Blocking: true,
			Check:    qaicCheckCard,
		})
	}
	if len(noHwmon) > 0 {
		problems = append(problems, qaicHostProblem{
			Message:  fmt.Sprintf("No qaic temperature sensor was found for %s.", strings.Join(noHwmon, ", ")),
			Fix:      "Load the kernel module that creates the qaic hwmon, so the card's temperature can be watched. " + qaicHostSetupDoc,
			Blocking: true,
			Check:    qaicCheckCard,
		})
	}

	ready.DockerUsable = dockerErr == nil
	if dockerErr != nil {
		problems = append(problems, qaicHostProblem{
			Message:  dockerErr.Error(),
			Fix:      "Install Docker, and add this user to the docker group. " + qaicHostSetupDoc,
			Blocking: true,
			Check:    qaicCheckDocker,
		})
	}

	ready.P2PAllowed = acsRedirectDisabled(socs, cmdline)
	if ready.CardPresent && !ready.P2PAllowed {
		ports := make([]string, 0, len(socs))
		for _, soc := range socs {
			ports = append(ports, soc.UpstreamPort)
		}
		problems = append(problems, qaicHostProblem{
			Message: "The card's SoCs cannot reach each other, because ACS redirect is on in its PCIe switch. Only models compiled for 1 SoC can run.",
			Fix:     "Add the kernel parameter pci=disable_acs_redir=" + strings.Join(ports, ";") + " and reboot. " + qaicHostSetupDoc,
			Check:   qaicCheckP2P,
		})
	}
	return ready, problems
}

// acsRedirectDisabled reports whether the kernel command line turns ACS
// redirect off for every SoC's upstream port. One SoC sends nothing to another,
// so it needs nothing.
func acsRedirectDisabled(socs []accel.QAICSoCState, cmdline string) bool {
	if len(socs) < 2 {
		return true
	}
	addresses, ids := disabledACSRedirect(cmdline)
	for _, soc := range socs {
		if soc.UpstreamPort == "" {
			return false
		}
		if !addresses[soc.UpstreamPort] && !ids[soc.UpstreamID] {
			return false
		}
	}
	return true
}

// disabledACSRedirect parses every pci=...disable_acs_redir=<list> option. The
// list holds PCI addresses, with or without the "0000:" domain, and
// "pci:vendor:device" ids; entries are separated by ";".
func disabledACSRedirect(cmdline string) (map[string]bool, map[string]bool) {
	addresses := map[string]bool{}
	ids := map[string]bool{}
	for _, token := range strings.Fields(cmdline) {
		token = strings.Trim(token, `"'`)
		value, ok := strings.CutPrefix(token, "pci=")
		if !ok {
			continue
		}
		for _, option := range strings.Split(value, ",") {
			list, ok := strings.CutPrefix(option, "disable_acs_redir=")
			if !ok {
				continue
			}
			for _, entry := range strings.Split(list, ";") {
				entry = strings.ToLower(strings.TrimSpace(entry))
				if id, ok := strings.CutPrefix(entry, "pci:"); ok {
					parts := strings.Split(id, ":")
					if len(parts) >= 2 {
						ids[parts[0]+":"+parts[1]] = true
					}
					continue
				}
				if strings.Count(entry, ":") == 1 {
					entry = "0000:" + entry
				}
				addresses[entry] = true
			}
		}
	}
	return addresses, ids
}
