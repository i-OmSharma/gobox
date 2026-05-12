// Package network sets up Linux bridge + veth pair for container network isolation.
//
// How container networking works in veil:
//
//	Host side                        Container side
//	────────────────────────────────────────────────
//	[veil0 bridge: 10.88.0.1/16]
//	      │
//	[veth-<pid>]  ←── veth pair ──→  [eth0: 10.88.0.2/16]
//	      │
//	iptables MASQUERADE → host NIC → internet
//
// SetupBridge: run once — idempotent, skipped if veil0 already exists.
// SetupVeth:   run per container start (inside the SIGSTOP window).
// CleanupVeth: run per container exit.

package network

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/vishvananda/netlink"
)

const (
	BridgeName    = "veil0"        // Linux bridge name on host
	BridgeIP      = "10.88.0.1/16" // Bridge IP — also the container's default gateway.
	BridgeNetwork = "10.88.0.0/16" // Subnet used in the MASQUERADE rule; must not overlap with host routes.
	ContainerIP   = "10.88.0.2/16" // Static IP for eth0 inside the container (v0.1 supports one container at a time).
	GatewayIP     = "10.88.0.1"    // Default route written inside the container's netns.

	// 10.88.0.0/16 is chosen to avoid conflicts with Docker's default pool (172.17–172.20)
	// and libvirt's range (192.168.122.0/24). Podman also uses 10.88.0.0/16 by default.
)

// SetupBridge creates the "veil0" Linux bridge on the host.
// Think of the bridge like a virtual network switch — all containers plug into it.
// Safe to call multiple times; returns early if bridge already exists.
func SetupBridge() error {
	// If the bridge is already up from a previous container run, skip creation.
	if _, err := netlink.LinkByName(BridgeName); err == nil {
		return nil
	}

	// Create the bridge interface (like "ip link add veil0 type bridge")
	bridge := &netlink.Bridge{
		LinkAttrs: netlink.LinkAttrs{Name: BridgeName},
	}
	if err := netlink.LinkAdd(bridge); err != nil {
		return fmt.Errorf("create bridge %s: %w", BridgeName, err)
	}

	// Assign 172.20.0.1/16 to the bridge so the host can communicate with containers.
	// This IP becomes the gateway that containers route traffic through.
	addr, err := netlink.ParseAddr(BridgeIP)
	if err != nil {
		return fmt.Errorf("parse bridge IP: %w", err)
	}
	if err := netlink.AddrAdd(bridge, addr); err != nil {
		return fmt.Errorf("add IP to bridge: %w", err)
	}

	// Bring the bridge interface up (like "ip link set veil0 up")
	if err := netlink.LinkSetUp(bridge); err != nil {
		return fmt.Errorf("bring bridge up: %w", err)
	}

	// Register veil0 with firewalld's trusted zone so packets aren't blocked.
	// On Fedora/RHEL, firewalld is the default firewall and unknown interfaces
	// default to the restrictive FedoraWorkstation zone which drops forwarded traffic.
	// firewall-cmd is called with --permanent=false (session only) to avoid persisting
	// state across reboots — veil0 is recreated on each session anyway.
	// This call is a no-op if firewalld is not running (e.g. Ubuntu/Debian).
	fwCmd := exec.Command("firewall-cmd", "--zone=trusted", "--add-interface="+BridgeName)
	if out, err := fwCmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			fmt.Printf("[network] firewalld: %s\n", msg)
		}
	}

	// Enable IP forwarding so the kernel routes packets between bridge and host interfaces.
	// Without this, packets arriving at veil0 from containers are dropped.
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644); err != nil {
		return fmt.Errorf("enable IP forwarding: %w", err)
	}

	// Add iptables MASQUERADE rule so containers can reach the internet.
	// MASQUERADE rewrites the source IP of outbound packets to the host's public IP.
	// "-C" checks if rule already exists before adding it (avoids duplicates).
	check := exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING",
		"-s", BridgeNetwork, "-j", "MASQUERADE")
	if err := check.Run(); err != nil {
		// Rule doesn't exist yet — add it.
		add := exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING",
			"-s", BridgeNetwork, "-j", "MASQUERADE")
		if out, err := add.CombinedOutput(); err != nil {
			return fmt.Errorf("add iptables MASQUERADE: %w, output: %s", err, string(out))
		}
	}

	fmt.Printf("[network] Bridge %s up at %s\n", BridgeName, BridgeIP)
	return nil
}

// SetupVeth creates a virtual ethernet (veth) pair and wires the container into the bridge.
//
// A veth pair is like a network cable with two ends:
//   - Host end (veth-<pid>): plugged into the veil0 bridge
//   - Container end (eth0):  moved into the container's network namespace
//
// This must be called AFTER cmd.Start() (so the container PID exists)
// but BEFORE SIGCONT (so the container hasn't run its command yet).
// That window is the same one used for cgroup setup.
func SetupVeth(containerPID int, containerIP string) error {
	hostVeth := fmt.Sprintf("veth-%d", containerPID) // e.g. "veth-12345"
	contVeth := "eth0"                                // always "eth0" inside the container

	// Create the veth pair — both ends start on the host side.
	// (like "ip link add veth-<pid> type veth peer name eth0")
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: hostVeth},
		PeerName:  contVeth,
	}
	if err := netlink.LinkAdd(veth); err != nil {
		return fmt.Errorf("create veth pair: %w", err)
	}

	// Grab references to both ends by name.
	hostLink, err := netlink.LinkByName(hostVeth)
	if err != nil {
		return fmt.Errorf("find host veth %s: %w", hostVeth, err)
	}
	contLink, err := netlink.LinkByName(contVeth)
	if err != nil {
		return fmt.Errorf("find container veth %s: %w", contVeth, err)
	}

	// Attach the host end to our bridge — it becomes a port on the virtual switch.
	bridge, err := netlink.LinkByName(BridgeName)
	if err != nil {
		return fmt.Errorf("find bridge %s: %w", BridgeName, err)
	}
	if err := netlink.LinkSetMaster(hostLink, bridge); err != nil {
		return fmt.Errorf("attach veth to bridge: %w", err)
	}

	// Bring the host end up so packets can flow.
	if err := netlink.LinkSetUp(hostLink); err != nil {
		return fmt.Errorf("bring host veth up: %w", err)
	}

	// Move the container end (eth0) into the container's network namespace.
	// After this call, eth0 disappears from the host and appears only inside
	// the container — that's what gives network isolation.
	if err := netlink.LinkSetNsPid(contLink, containerPID); err != nil {
		return fmt.Errorf("move veth to container netns: %w", err)
	}

	// Configure eth0 inside the container using nsenter.
	// nsenter -t <pid> -n enters the network namespace of process <pid>.
	// We set IP, bring the interface up, and add a default route — all from host side,
	// while the container process is still SIGSTOP'd.
	nsenter := exec.Command("nsenter", "-t", fmt.Sprintf("%d", containerPID), "-n",
		"sh", "-c", fmt.Sprintf(
			"ip link set lo up && ip link set eth0 up && ip addr add %s dev eth0 && ip route add default via %s",
			containerIP, GatewayIP,
		),
	)
	if out, err := nsenter.CombinedOutput(); err != nil {
		return fmt.Errorf("configure container netns: %w, output: %s", err, string(out))
	}

	fmt.Printf("[network] veth pair ready: host=%s ↔ container=eth0 (%s)\n", hostVeth, containerIP)
	return nil
}

// CleanupVeth deletes the host-side veth interface.
// When the host end is deleted, the kernel automatically removes the container end too.
// Safe to call even if the veth was already cleaned up (e.g. container crashed).
func CleanupVeth(containerPID int) error {
	hostVeth := fmt.Sprintf("veth-%d", containerPID)
	link, err := netlink.LinkByName(hostVeth)
	if err != nil {
		// Already gone — container exit cleaned it up or it was never created.
		return nil
	}
	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("delete veth %s: %w", hostVeth, err)
	}
	fmt.Printf("[network] Cleaned up %s\n", hostVeth)
	return nil
}

// GetContainerIP returns the IP to assign to eth0 inside the container.
// v0.1.1: static assignment — supports one container at a time.
// v0.2.0 TODO: implement a proper IPAM pool to support concurrent containers.
func GetContainerIP() string {
	return ContainerIP
}

// SetupPortForward adds iptables rules to forward hostPort → containerIP:containerPort.
// containerIP may include CIDR notation (e.g. "10.88.0.2/16"); the prefix is stripped.
func SetupPortForward(hostPort, containerPort, containerIP string) error {
	// Strip CIDR suffix — iptables DNAT destination needs bare IP.
	ip := strings.SplitN(containerIP, "/", 2)[0]

	// route_localnet=1 allows the kernel to route packets that originated on lo
	// (127.0.0.1) to non-loopback interfaces after OUTPUT DNAT rewrites the destination.
	// Without this, curl localhost:hostPort would be silently dropped by the kernel.
	_ = os.WriteFile("/proc/sys/net/ipv4/conf/all/route_localnet", []byte("1"), 0644)

	// PREROUTING DNAT: rewrite destination IP+port for incoming packets.
	preArgs := []string{"-t", "nat", "-A", "PREROUTING",
		"-p", "tcp", "--dport", hostPort,
		"-j", "DNAT", "--to-destination", ip + ":" + containerPort}
	preCheck := exec.Command("iptables", append([]string{"-t", "nat", "-C", "PREROUTING",
		"-p", "tcp", "--dport", hostPort,
		"-j", "DNAT", "--to-destination", ip + ":" + containerPort})...)
	if err := preCheck.Run(); err != nil {
		if out, err := exec.Command("iptables", preArgs...).CombinedOutput(); err != nil {
			return fmt.Errorf("iptables DNAT %s->%s:%s: %w, output: %s", hostPort, ip, containerPort, err, string(out))
		}
	}

	// OUTPUT DNAT: rewrite destination for packets originating on the host itself.
	// PREROUTING only handles traffic entering from outside; localhost curl/wget
	// goes through OUTPUT and would miss the DNAT without this rule.
	outArgs := []string{"-t", "nat", "-A", "OUTPUT",
		"-p", "tcp", "--dport", hostPort,
		"-j", "DNAT", "--to-destination", ip + ":" + containerPort}
	outCheck := exec.Command("iptables", "-t", "nat", "-C", "OUTPUT",
		"-p", "tcp", "--dport", hostPort,
		"-j", "DNAT", "--to-destination", ip+":"+containerPort)
	if err := outCheck.Run(); err != nil {
		if out, err := exec.Command("iptables", outArgs...).CombinedOutput(); err != nil {
			return fmt.Errorf("iptables OUTPUT DNAT %s->%s:%s: %w, output: %s", hostPort, ip, containerPort, err, string(out))
		}
	}

	// MASQUERADE on veil0: when the host sends a port-forwarded packet to the container,
	// the source is 127.0.0.1. Inside the container, 127.0.0.1 resolves to the container's
	// own loopback — replies would go to the wrong lo and never return to the host.
	// Masquerading replaces src=127.0.0.1 with src=10.88.0.1 (bridge IP) so the container
	// routes replies correctly via its default gateway.
	masqCheck := exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING",
		"-o", BridgeName, "-j", "MASQUERADE")
	if err := masqCheck.Run(); err != nil {
		if out, err := exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING",
			"-o", BridgeName, "-j", "MASQUERADE").CombinedOutput(); err != nil {
			return fmt.Errorf("iptables MASQUERADE on %s: %w, output: %s", BridgeName, err, string(out))
		}
	}

	// FORWARD ACCEPT: allow forwarded packets destined for the container.
	fwdArgs := []string{"-A", "FORWARD",
		"-p", "tcp", "-d", ip, "--dport", containerPort, "-j", "ACCEPT"}
	fwdCheck := exec.Command("iptables", "-C", "FORWARD",
		"-p", "tcp", "-d", ip, "--dport", containerPort, "-j", "ACCEPT")
	if err := fwdCheck.Run(); err != nil {
		if out, err := exec.Command("iptables", fwdArgs...).CombinedOutput(); err != nil {
			return fmt.Errorf("iptables FORWARD %s:%s: %w, output: %s", ip, containerPort, err, string(out))
		}
	}

	fmt.Printf("[network] port forward: host:%s → %s:%s\n", hostPort, ip, containerPort)
	return nil
}

// CleanupPortForward removes the iptables rules added by SetupPortForward.
func CleanupPortForward(hostPort, containerPort, containerIP string) error {
	ip := strings.SplitN(containerIP, "/", 2)[0]

	preCheck := exec.Command("iptables", "-t", "nat", "-C", "PREROUTING",
		"-p", "tcp", "--dport", hostPort,
		"-j", "DNAT", "--to-destination", ip+":"+containerPort)
	if err := preCheck.Run(); err == nil {
		del := exec.Command("iptables", "-t", "nat", "-D", "PREROUTING",
			"-p", "tcp", "--dport", hostPort,
			"-j", "DNAT", "--to-destination", ip+":"+containerPort)
		if out, err := del.CombinedOutput(); err != nil {
			return fmt.Errorf("remove DNAT rule: %w, output: %s", err, string(out))
		}
	}

	outCheck := exec.Command("iptables", "-t", "nat", "-C", "OUTPUT",
		"-p", "tcp", "--dport", hostPort,
		"-j", "DNAT", "--to-destination", ip+":"+containerPort)
	if err := outCheck.Run(); err == nil {
		del := exec.Command("iptables", "-t", "nat", "-D", "OUTPUT",
			"-p", "tcp", "--dport", hostPort,
			"-j", "DNAT", "--to-destination", ip+":"+containerPort)
		if out, err := del.CombinedOutput(); err != nil {
			return fmt.Errorf("remove OUTPUT DNAT rule: %w, output: %s", err, string(out))
		}
	}

	fwdCheck := exec.Command("iptables", "-C", "FORWARD",
		"-p", "tcp", "-d", ip, "--dport", containerPort, "-j", "ACCEPT")
	if err := fwdCheck.Run(); err == nil {
		del := exec.Command("iptables", "-D", "FORWARD",
			"-p", "tcp", "-d", ip, "--dport", containerPort, "-j", "ACCEPT")
		if out, err := del.CombinedOutput(); err != nil {
			return fmt.Errorf("remove FORWARD rule: %w, output: %s", err, string(out))
		}
	}

	fmt.Printf("[network] removed port forward: host:%s → %s:%s\n", hostPort, ip, containerPort)
	return nil
}
