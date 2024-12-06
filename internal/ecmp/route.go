package ecmp

import (
	"context"
	"fmt"
	"log"

	ovn "github.com/kubeovn/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/kubeovn/kube-ovn/pkg/ovs"
	ovnnb "github.com/kubeovn/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/kubeovn/kube-ovn/pkg/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	RADD = 1
	RUPD = 2
	RDEL = 3
)

func HandleGwRoute(ctx context.Context, eventType int, gw *ovn.VpcNatGateway, prefix string, rclient client.Client, nbClient ovs.OVNNbClient) error {
	log.Println("Entering HandleGwRoute function")
	defer log.Println("Exiting HandleGwRoute function")

	if gw == nil {
		return fmt.Errorf("gateway cannot be nil")
	}
	if prefix == "" {
		return fmt.Errorf("prefix cannot be empty")
	}

	vpc := &ovn.Vpc{}
	log.Printf("Fetching VPC with name: %s\n", gw.Spec.Vpc)
	err := rclient.Get(ctx, client.ObjectKey{Name: gw.Spec.Vpc}, vpc)
	if err != nil {
		return fmt.Errorf("error getting VPC: %w", err)
	}

	lr := vpc.Name
	log.Printf("Logical router name resolved: %s\n", lr)

	c := NewApiInterface(nbClient)
	if eventType == RADD || eventType == RUPD {
		err = c.AddOrUpdateECMPRoute(lr, prefix, gw.Spec.LanIP)
	} else if eventType == RDEL {
		err = c.DeleteECMPRoute(lr, prefix, gw.Spec.LanIP)
	} else {
		log.Println("Unknown Event Type")
		err = fmt.Errorf("Unknown Event Type")
		return err
	}
	if err != nil {
		log.Printf("Failed to add/update ECMP route: %v\n", err)
		return err
	}

	log.Println("Successfully handled gateway route")
	return nil
}

type api struct {
	nbClient ovs.OVNNbClient
}

func NewApiInterface(c ovs.OVNNbClient) Interface {
	log.Println("Creating new API interface instance")
	return &api{nbClient: c}
}

func (c *api) AddOrUpdateECMPRoute(logicalRouterName, cidr string, nextHops ...string) error {
	log.Printf("AddOrUpdateECMPRoute called with router: %s, CIDR: %s, NextHops: %v\n", logicalRouterName, cidr, nextHops)

	if logicalRouterName == "" || cidr == "" {
		return fmt.Errorf("logicalRouterName and CIDR must not be empty")
	}

	policy := ovnnb.LogicalRouterStaticRoutePolicyDstIP
	log.Println("Adding logical router static route...")

	err := c.nbClient.AddLogicalRouterStaticRoute(logicalRouterName, util.MainRouteTable, policy, cidr, nil, nextHops...)
	if err != nil {
		log.Printf("Error adding logical router static route: %v\n", err)
		return err
	}

	log.Println("Successfully added ECMP route")
	return nil
}

func (c *api) DeleteECMPRoute(logicalRouterName, cidr string, nextHops ...string) error {
	log.Printf("DeleteECMPRoute called with router: %s, CIDR: %s, NextHops: %v\n", logicalRouterName, cidr, nextHops)

	if logicalRouterName == "" || cidr == "" {
		return fmt.Errorf("logicalRouterName and CIDR must not be empty")
	}

	policy := ovnnb.LogicalRouterStaticRoutePolicyDstIP
	rtb := util.MainRouteTable

	for _, nextHop := range nextHops {
		if nextHop == "" {
			log.Println("Skipping empty nextHop")
			continue
		}

		log.Printf("Deleting static route for nextHop: %s\n", nextHop)
		err := c.nbClient.DeleteLogicalRouterStaticRoute(logicalRouterName, &rtb, &policy, cidr, nextHop)
		if err != nil {
			log.Printf("Error deleting logical router static route: %v\n", err)
			return err
		}
	}

	log.Println("Successfully deleted ECMP route")
	return nil
}

func ValidateParameters(params ...string) error {
	for _, param := range params {
		if param == "" {
			return fmt.Errorf("parameter cannot be empty")
		}
	}
	return nil
}

func debugInfo(info string) {
	log.Printf("Debug info: %s\n", info)
}

func LogExecutionTime(action string, startTime, endTime int64) {
	log.Printf("Action %s took %d ms to execute\n", action, endTime-startTime)
}
