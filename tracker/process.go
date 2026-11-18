package tracker

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ProcEntry struct {
	PID     int
	Name    string
	CmdLine string
}

type ProcMapper struct {
	mu         sync.RWMutex
	portMap    map[uint16]map[uint8]ProcEntry // port -> protocol -> entry
	lastUpdate time.Time
	interval   time.Duration
}

func NewProcMapper() *ProcMapper {
	return &ProcMapper{
		portMap:  make(map[uint16]map[uint8]ProcEntry),
		interval: 5 * time.Second,
	}
}

func (pm *ProcMapper) Update() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if time.Since(pm.lastUpdate) < pm.interval {
		return nil
	}

	// Step 1: Build socket inode -> PID mapping from /proc/*/fd/
	inodeToPID, err := buildSocketMap()
	if err != nil {
		return err
	}

	// Step 2: Read system-wide /proc/net/tcp{,6} and /proc/net/udp{,6}
	// to map (local port, protocol) -> inode
	type portInode struct {
		port     uint16
		protocol uint8
		inode    uint64
	}

	var portInodes []portInode

	netFiles := []struct {
		path     string
		protocol uint8
	}{
		{"/proc/net/tcp", 6},
		{"/proc/net/tcp6", 6},
		{"/proc/net/udp", 17},
		{"/proc/net/udp6", 17},
	}

	for _, nf := range netFiles {
		data, err := os.ReadFile(nf.path)
		if err != nil {
			continue
		}

		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[0] == "sl" {
				continue
			}

			// Parse local address: HEX_IP:HEX_PORT
			localPart := fields[1]
			parts := strings.Split(localPart, ":")
			if len(parts) < 2 {
				continue
			}

			portHex := parts[len(parts)-1]
			if len(portHex) > 4 {
				portHex = portHex[len(portHex)-4:]
			}
			portVal, err := strconv.ParseUint(portHex, 16, 16)
			if err != nil {
				continue
			}

			// State: field[3] - only care about established/listen
			// For our purposes any state is fine

			// Inode: field[9]
			inode, err := strconv.ParseUint(fields[9], 10, 64)
			if err != nil || inode == 0 {
				continue
			}

			portInodes = append(portInodes, portInode{
				port:     uint16(portVal),
				protocol: nf.protocol,
				inode:    inode,
			})
		}
	}

	// Step 3: Cross-reference port/inode with inode/PID
	newPortMap := make(map[uint16]map[uint8]ProcEntry)

	for _, pi := range portInodes {
		pid, found := inodeToPID[pi.inode]
		if !found {
			continue
		}

		name, cmd := getProcessInfo(pid)
		if _, ok := newPortMap[pi.port]; !ok {
			newPortMap[pi.port] = make(map[uint8]ProcEntry)
		}

		// Only set if not already set (prefer first found)
		if _, exists := newPortMap[pi.port][pi.protocol]; !exists {
			newPortMap[pi.port][pi.protocol] = ProcEntry{
				PID:     pid,
				Name:    name,
				CmdLine: cmd,
			}
		}
	}

	pm.portMap = newPortMap
	pm.lastUpdate = time.Now()
	return nil
}

func (pm *ProcMapper) Lookup(port uint16, protocol uint8) (ProcEntry, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if protoMap, ok := pm.portMap[port]; ok {
		entry, found := protoMap[protocol]
		return entry, found
	}
	return ProcEntry{}, false
}

func (pm *ProcMapper) SetInterval(d time.Duration) {
	pm.mu.Lock()
	pm.interval = d
	pm.mu.Unlock()
}

func buildSocketMap() (map[uint64]int, error) {
	sockMap := make(map[uint64]int)

	procDir, err := os.Open("/proc")
	if err != nil {
		return nil, err
	}
	defer procDir.Close()

	for {
		entries, err := procDir.Readdir(100)
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			pid, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}

			fdDir := fmt.Sprintf("/proc/%d/fd", pid)
			fdEntries, err := os.ReadDir(fdDir)
			if err != nil {
				continue
			}

			for _, fd := range fdEntries {
				link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
				if err != nil {
					continue
				}
				if strings.HasPrefix(link, "socket:[") && strings.HasSuffix(link, "]") {
					inodeStr := strings.TrimPrefix(link, "socket:[")
					inodeStr = strings.TrimSuffix(inodeStr, "]")
					if inode, err := strconv.ParseUint(inodeStr, 10, 64); err == nil {
						if _, exists := sockMap[inode]; !exists {
							sockMap[inode] = pid
						}
					}
				}
			}
		}
	}

	return sockMap, nil
}

func getProcessInfo(pid int) (name, cmdLine string) {
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		name = strings.TrimSpace(string(data))
	}
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		cmdLine = strings.ReplaceAll(string(data), "\x00", " ")
		cmdLine = strings.TrimSpace(cmdLine)
	}
	if name == "" {
		name = "?"
	}
	return name, cmdLine
}

func (p ProcEntry) String() string {
	if p.CmdLine != "" {
		return fmt.Sprintf("%d: %s", p.PID, p.CmdLine)
	}
	if p.Name != "" {
		return fmt.Sprintf("%d: %s", p.PID, p.Name)
	}
	return fmt.Sprintf("%d", p.PID)
}
