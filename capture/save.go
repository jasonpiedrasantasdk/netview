package capture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
)

func SavePcap(packets []SavedPacket) (string, error) {
	path, err := choosePath()
	if err != nil {
		return "", err
	}

	if err := writePcap(packets, path); err != nil {
		return "", fmt.Errorf("write pcap: %w", err)
	}

	return path, nil
}

func writePcap(packets []SavedPacket, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := pcapgo.NewWriter(f)
	if err := w.WriteFileHeader(65536, layers.LinkTypeEthernet); err != nil {
		return err
	}

	for _, p := range packets {
		ci := gopacket.CaptureInfo{
			Timestamp:     p.Timestamp,
			Length:        len(p.Data),
			CaptureLength: len(p.Data),
		}
		if err := w.WritePacket(ci, p.Data); err != nil {
			return err
		}
	}

	return nil
}

func choosePath() (string, error) {
	if path := tryFileDialog(); path != "" {
		return path, nil
	}
	return defaultPath(), nil
}

func tryFileDialog() string {
	timestamp := time.Now().Format("2006-01-02T15-04-05")
	filename := fmt.Sprintf("netview-%s.pcap", timestamp)

	// Try zenity (GNOME)
	cmd := exec.Command("zenity", "--file-selection", "--save",
		"--filename="+filename,
		"--title=Save NetView Capture",
		"--file-filter=PCAP files *.pcap")
	out, err := cmd.Output()
	if err == nil {
		path := strings.TrimSpace(string(out))
		if path != "" {
			if !strings.HasSuffix(path, ".pcap") {
				path += ".pcap"
			}
			return path
		}
	}

	// Try kdialog (KDE)
	cmd = exec.Command("kdialog", "--getsavefilename", filename,
		"*.pcap|PCAP files")
	out, err = cmd.Output()
	if err == nil {
		path := strings.TrimSpace(string(out))
		if path != "" {
			if !strings.HasSuffix(path, ".pcap") {
				path += ".pcap"
			}
			return path
		}
	}

	return ""
}

func defaultPath() string {
	home, err := os.UserHomeDir()
	if err == nil {
		candidate := filepath.Join(home, "netViewSaves")
		if err := os.MkdirAll(candidate, 0755); err == nil {
			return filepath.Join(candidate, fmt.Sprintf("netview-%s.pcap",
				time.Now().Format("2006-01-02T15-04-05")))
		}
	}

	candidate := "netViewSaves"
	os.MkdirAll(candidate, 0755)
	return filepath.Join(candidate, fmt.Sprintf("netview-%s.pcap",
		time.Now().Format("2006-01-02T15-04-05")))
}

func OpenInFileManager(path string) {
	dir := filepath.Dir(path)
	exec.Command("xdg-open", dir).Start()
}
