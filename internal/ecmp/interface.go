package ecmp

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

type Interface interface {
	AddOrUpdateECMPRoute(lr, prefix string, nextHops ...string) error
	DeleteECMPRoute(lr, prefix string, nextHops ...string) error
}

func NewDefaultInterface() Interface {
	log.Println("Creating a new default interface instance")
	return &ctl{}
}

type ctl struct{}

func (c *ctl) AddOrUpdateECMPRoute(logicalRouterName, cidr string, nextHops ...string) error {
	if err := c.ValidateRouteParameters(logicalRouterName, cidr, nextHops...); err != nil {
		return err
	}
	for _, nextHop := range nextHops {
		cmd := exec.Command("ovn-nbctl", "--wait=hv",
			"lr-route-add", logicalRouterName, cidr, nextHop, "--ecmp-symmetric-reply=true")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to add/update ECMP route: %s, output: %s", err, string(output))
		}
	}
	// c.PrintRouteSummary(logicalRouterName)
	return nil
}

func (c *ctl) DeleteECMPRoute(logicalRouterName, cidr string, nextHops ...string) error {
	if err := c.ValidateRouteParameters(logicalRouterName, cidr, nextHops...); err != nil {
		return err
	}
	for _, nextHop := range nextHops {
		cmd := exec.Command("ovn-nbctl", "--wait=hv",
			"lr-route-del", logicalRouterName, cidr, nextHop)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to delete ECMP route: %s, output: %s", err, string(output))
		}
	}
	// c.PrintRouteSummary(logicalRouterName)
	return nil
}

func (c *ctl) ValidateRouteParameters(lr, prefix string, nextHops ...string) error {
	log.Println("Validating route parameters...")
	if lr == "" || prefix == "" {
		return fmt.Errorf("logical router name and prefix must not be empty")
	}
	for _, hop := range nextHops {
		if strings.TrimSpace(hop) == "" {
			return fmt.Errorf("next hop cannot be an empty string")
		}
	}
	log.Println("Route parameters validation passed")
	return nil
}

func (c *ctl) PrintRouteSummary(lr string) {
	log.Printf("Info: %s\n", lr)
}
