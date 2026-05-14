package port

import (
	"testing"
)

const (
	protoTCP  = "tcp"
	protoUnix = "unix"

	addrLocalhost8080 = "localhost:8080"
	addrLocalhost3000 = "localhost:3000"
	testUnixSocket    = "/var/run/app.sock"
)

func TestParsePortSpec_BasicPorts(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Mapping
	}{
		{
			name:  "bare port",
			input: "8080",
			want: Mapping{
				Host:      Address{Protocol: protoTCP, Address: addrLocalhost8080},
				Container: Address{Protocol: protoTCP, Address: addrLocalhost8080},
			},
		},
		{
			name:  "host port to container port",
			input: "8080:3000",
			want: Mapping{
				Host:      Address{Protocol: protoTCP, Address: addrLocalhost8080},
				Container: Address{Protocol: protoTCP, Address: addrLocalhost3000},
			},
		},
		{
			name:  "unix socket",
			input: testUnixSocket,
			want: Mapping{
				Host:      Address{Protocol: protoUnix, Address: testUnixSocket},
				Container: Address{Protocol: protoUnix, Address: testUnixSocket},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePortSpec(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParsePortSpec_Hostnames(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Mapping
	}{
		{
			name:  "IP host three-part",
			input: "127.0.0.1:8080:3000",
			want: Mapping{
				Host:      Address{Protocol: protoTCP, Address: "127.0.0.1:8080"},
				Container: Address{Protocol: protoTCP, Address: addrLocalhost3000},
			},
		},
		{
			name:  "localhost explicit three-part",
			input: "localhost:8080:3000",
			want: Mapping{
				Host:      Address{Protocol: protoTCP, Address: addrLocalhost8080},
				Container: Address{Protocol: protoTCP, Address: addrLocalhost3000},
			},
		},
		{
			name:  "hostname host",
			input: "database.internal:5432:5432",
			want: Mapping{
				Host:      Address{Protocol: protoTCP, Address: "database.internal:5432"},
				Container: Address{Protocol: protoTCP, Address: "localhost:5432"},
			},
		},
		{
			name:  "container hostname in three-part spec",
			input: "8080:redis:6379",
			want: Mapping{
				Host:      Address{Protocol: protoTCP, Address: addrLocalhost8080},
				Container: Address{Protocol: protoTCP, Address: "redis:6379"},
			},
		},
		{
			name:  "four-part full spec with hostnames",
			input: "myhost:8080:mycontainer:3000",
			want: Mapping{
				Host:      Address{Protocol: protoTCP, Address: "myhost:8080"},
				Container: Address{Protocol: protoTCP, Address: "mycontainer:3000"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePortSpec(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParsePortSpec_Errors(t *testing.T) {
	_, err := ParsePortSpec("a:b:c:d:e")
	if err == nil {
		t.Fatal("expected error for too many parts")
	}
}

func TestToAddress_TCP(t *testing.T) {
	tests := []struct {
		name string
		host string
		port string
		want Address
	}{
		{
			"empty host defaults to localhost",
			"",
			"8080",
			Address{Protocol: protoTCP, Address: addrLocalhost8080},
		},
		{defaultHost, defaultHost, "3000", Address{Protocol: protoTCP, Address: addrLocalhost3000}},
		{
			"IP address", "192.168.1.1", "443",
			Address{Protocol: protoTCP, Address: "192.168.1.1:443"},
		},
		{
			"hostname",
			"database.internal",
			"5432",
			Address{Protocol: protoTCP, Address: "database.internal:5432"},
		},
		{"short hostname", "db", "5432", Address{Protocol: protoTCP, Address: "db:5432"}},
		{"IPv6 address", "::1", "8080", Address{Protocol: protoTCP, Address: "::1:8080"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toAddress(tt.host, tt.port)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestToAddress_Unix(t *testing.T) {
	got, err := toAddress("", testUnixSocket)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Address{Protocol: protoUnix, Address: testUnixSocket}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestToAddress_HostWithUnixErrors(t *testing.T) {
	_, err := toAddress("myhost", testUnixSocket)
	if err == nil {
		t.Fatal("expected error for host with unix socket")
	}
}

func TestSplitParts(t *testing.T) {
	type result struct{ hostIP, hostPort, contIP, contPort string }

	tests := []struct {
		name  string
		input string
		want  result
	}{
		{"single port", "8080", result{"", "8080", "", "8080"}},
		{"two parts", "8080:3000", result{"", "8080", "", "3000"}},
		{"three parts numeric middle", "myhost:8080:3000", result{"myhost", "8080", "", "3000"}},
		{"three parts hostname middle", "8080:redis:6379", result{"", "8080", "redis", "6379"}},
		{
			"three parts IP middle",
			"8080:192.168.1.1:3000",
			result{"", "8080", "192.168.1.1", "3000"},
		},
		{
			"three parts localhost middle",
			"8080:localhost:3000",
			result{"", "8080", defaultHost, "3000"},
		},
		{
			"four parts",
			"myhost:8080:mycontainer:3000",
			result{"myhost", "8080", "mycontainer", "3000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostIP, hostPort, contIP, contPort, err := splitParts(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := result{hostIP, hostPort, contIP, contPort}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSplitParts_Errors(t *testing.T) {
	_, _, _, _, err := splitParts("a:b:c:d:e")
	if err == nil {
		t.Fatal("expected error for five parts")
	}
}
