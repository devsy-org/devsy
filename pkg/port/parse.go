package port

import (
	"fmt"
	"strconv"
	"strings"
)

type Address struct {
	Protocol string
	Address  string
}

type Mapping struct {
	Host      Address
	Container Address
}

func ParsePortSpec(port string) (Mapping, error) {
	hostIP, hostPort, containerIP, containerPort, err := splitParts(port)
	if err != nil {
		return Mapping{}, err
	}

	hostAddress, err := toAddress(hostIP, hostPort)
	if err != nil {
		return Mapping{}, fmt.Errorf("parse host address: %w", err)
	}

	containerAddress, err := toAddress(containerIP, containerPort)
	if err != nil {
		return Mapping{}, fmt.Errorf("parse container address: %w", err)
	}

	return Mapping{
		Host:      hostAddress,
		Container: containerAddress,
	}, nil
}

func toAddress(host, port string) (Address, error) {
	_, err := strconv.Atoi(port)
	if err == nil {
		if host == "" {
			host = "localhost"
		}

		return Address{
			Protocol: "tcp",
			Address:  host + ":" + port,
		}, nil
	}

	if host != "" {
		return Address{}, fmt.Errorf("unexpected host for unix socket: %s", host)
	}

	return Address{
		Protocol: "unix",
		Address:  port,
	}, nil
}

func splitParts(rawport string) (string, string, string, string, error) {
	parts := strings.Split(rawport, ":")
	n := len(parts)
	containerport := parts[n-1]

	switch n {
	case 1:
		return "", containerport, "", containerport, nil
	case 2:
		return "", parts[0], "", containerport, nil
	case 3:
		// a:b:c — if middle token is non-numeric, it's a host/IP for the
		// container side: hostPort:containerHost:containerPort.
		// Otherwise it's hostHost:hostPort:containerPort.
		if _, err := strconv.Atoi(parts[1]); err != nil {
			return "", parts[0], parts[1], containerport, nil
		}

		return parts[0], parts[1], "", containerport, nil
	case 4:
		return parts[0], parts[1], parts[2], parts[3], nil
	default:
		return "", "", "", "", fmt.Errorf("unexpected port format: %s", rawport)
	}
}
