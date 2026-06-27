package port

import (
	"fmt"
	"strconv"
	"strings"
)

const defaultHost = "localhost"

type Address struct {
	Protocol string
	Address  string
}

type Mapping struct {
	Host      Address
	Container Address
}

func ParsePortSpec(port string) (Mapping, error) {
	parts, err := splitParts(port)
	if err != nil {
		return Mapping{}, err
	}

	return parts.toMapping()
}

func toAddress(host, port string) (Address, error) {
	_, err := strconv.Atoi(port)
	if err == nil {
		if host == "" {
			host = defaultHost
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

type parsedParts struct {
	hostIP        string
	hostPort      string
	containerIP   string
	containerPort string
}

func (p parsedParts) toMapping() (Mapping, error) {
	hostAddress, err := toAddress(p.hostIP, p.hostPort)
	if err != nil {
		return Mapping{}, fmt.Errorf("parse host address: %w", err)
	}

	containerAddress, err := toAddress(p.containerIP, p.containerPort)
	if err != nil {
		return Mapping{}, fmt.Errorf("parse container address: %w", err)
	}

	return Mapping{
		Host:      hostAddress,
		Container: containerAddress,
	}, nil
}

func splitParts(rawPort string) (parsedParts, error) {
	parts := strings.Split(rawPort, ":")
	n := len(parts)
	containerPort := parts[n-1]

	switch n {
	case 1:
		return parsedParts{hostPort: containerPort, containerPort: containerPort}, nil
	case 2:
		return parsedParts{hostPort: parts[0], containerPort: containerPort}, nil
	case 3:
		// a:b:c — if middle token is non-numeric, it's a host/IP for the
		// container side: hostPort:containerHost:containerPort.
		// Otherwise it's hostHost:hostPort:containerPort.
		if _, err := strconv.Atoi(parts[1]); err != nil {
			return parsedParts{hostPort: parts[0], containerIP: parts[1], containerPort: containerPort}, nil
		}

		return parsedParts{hostIP: parts[0], hostPort: parts[1], containerPort: containerPort}, nil
	case 4:
		return parsedParts{hostIP: parts[0], hostPort: parts[1], containerIP: parts[2], containerPort: parts[3]}, nil
	default:
		return parsedParts{}, fmt.Errorf("unexpected port format: %s", rawPort)
	}
}
