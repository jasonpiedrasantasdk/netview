package tracker

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

const (
	ProtoUnknown = "?"
	ProtoTCP     = "TCP"
	ProtoUDP     = "UDP"
	ProtoTLS     = "TLS"
	ProtoHTTP    = "HTTP"
	ProtoHTTPS   = "HTTPS"
	ProtoDNS     = "DNS"
	ProtoQUIC    = "QUIC"
	ProtoSSH     = "SSH"
	ProtoDHCP    = "DHCP"
)

type ProtocolInfo struct {
	Protocol       string // detected protocol name
	SNI            string // TLS SNI
	HTTPHost       string // HTTP Host header
	HTTPMethod     string // HTTP method (GET, POST, etc)
	HTTPPath       string // HTTP path
	DNSQuery       string // DNS query name
	IsTLS          bool
	IsHTTP         bool
	IsDNS          bool
	IsQUIC         bool
	TLSVersion     string
	ALPN           string
}

func DetectProtocol(payload []byte, srcPort, dstPort uint16, protocol uint8) ProtocolInfo {
	var info ProtocolInfo

	if protocol == 17 {
		info = detectUDPProtocol(payload, srcPort, dstPort)
		return info
	}
	if protocol != 6 {
		return info
	}

	info = detectTCPProtocol(payload, srcPort, dstPort)
	return info
}

func detectTCPProtocol(payload []byte, srcPort, dstPort uint16) ProtocolInfo {
	var info ProtocolInfo
	info.Protocol = ProtoTCP

	dstIsHTTPS := dstPort == 443 || dstPort == 8443
	srcIsHTTPS := srcPort == 443 || srcPort == 8443
	dstIsHTTP := dstPort == 80 || dstPort == 8080
	srcIsHTTP := srcPort == 80 || srcPort == 8080
	isSSH := dstPort == 22 || srcPort == 22

	if isSSH {
		if len(payload) >= 4 && string(payload[:4]) == "SSH-" {
			info.Protocol = ProtoSSH
		}
		return info
	}

	if len(payload) < 2 {
		if dstIsHTTPS {
			info.IsTLS = true
			info.Protocol = ProtoHTTPS
		} else if dstIsHTTP {
			info.IsHTTP = true
			info.Protocol = ProtoHTTP
		}
		return info
	}

	if dstIsHTTPS || srcIsHTTPS {
		if tryDetectTLS(payload, &info) {
			return info
		}
		if dstIsHTTPS {
			info.IsTLS = true
			info.Protocol = ProtoHTTPS
			return info
		}
	}

	if dstIsHTTP || srcIsHTTP {
		if tryDetectHTTP(payload, &info) {
			return info
		}
		if dstIsHTTP {
			info.IsHTTP = true
			info.Protocol = ProtoHTTP
			return info
		}
	}

	if dstIsHTTPS {
		tryDetectTLS(payload, &info)
	} else if dstIsHTTP {
		tryDetectHTTP(payload, &info)
	}

	return info
}

func detectUDPProtocol(payload []byte, srcPort, dstPort uint16) ProtocolInfo {
	var info ProtocolInfo
	info.Protocol = ProtoUDP

	isDNS := dstPort == 53 || srcPort == 53
	isQUIC := dstPort == 443 || srcPort == 443 || dstPort == 80 || srcPort == 80
	isDHCP := dstPort == 67 || srcPort == 67 || dstPort == 68 || srcPort == 68

	if isDNS {
		info.Protocol = ProtoDNS
		info.IsDNS = true
		if query := extractDNSQuery(payload); query != "" {
			info.DNSQuery = query
		}
		return info
	}

	if isQUIC && tryDetectQUIC(payload) {
		info.Protocol = ProtoQUIC
		info.IsQUIC = true
		return info
	}

	if isDHCP {
		info.Protocol = ProtoDHCP
		return info
	}

	return info
}

func tryDetectTLS(payload []byte, info *ProtocolInfo) bool {
	if len(payload) < 6 {
		return false
	}

	// TLS record: ContentType(1) + Version(2) + Length(2)
	if payload[0] != 0x16 && payload[0] != 0x17 {
		return false
	}

	contentType := payload[0]
	// 0x16 = Handshake, 0x17 = Application Data

	if contentType == 0x16 && len(payload) >= 6 {
		handshakeType := payload[5]
		// 0x01 = ClientHello
		if handshakeType == 0x01 {
			sni, alpn, version := extractTLSClientHello(payload[5:])
			info.SNI = sni
			info.ALPN = alpn
			info.TLSVersion = version
			info.IsTLS = true
			info.Protocol = ProtoTLS
			return true
		}
	}

	if contentType == 0x16 || contentType == 0x17 {
		info.IsTLS = true
		info.Protocol = ProtoTLS
		info.TLSVersion = tlsVersionString(payload[1], payload[2])
		return true
	}

	return false
}

func extractTLSClientHello(handshake []byte) (sni, alpn, version string) {
	if len(handshake) < 4 {
		return "", "", ""
	}

	// Skip handshake header (4 bytes)
	// handshake[0] = type (1 = ClientHello)
	// handshake[1:4] = length (3 bytes)
	if handshake[0] != 0x01 {
		return "", "", ""
	}

	handshakeLen := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
	_ = handshakeLen

	body := handshake[4:]
	if len(body) < 34 {
		return "", "", ""
	}

	// ClientHello version
	verMajor := body[0]
	verMinor := body[1]
	version = tlsVersionString(verMajor, verMinor)

	// Skip: version(2) + random(32) = 34 bytes
	// Actually version is at body[0:2], random at body[2:34]
	// session_id length at body[34]
	sidLen := int(body[34])
	offset := 35 + sidLen

	// cipher suites
	if offset+2 > len(body) {
		return version, "", ""
	}
	cipherLen := int(body[offset])<<8 | int(body[offset+1])
	offset += 2 + cipherLen

	// compression methods
	if offset+1 > len(body) {
		return version, "", ""
	}
	compLen := int(body[offset])
	offset += 1 + compLen

	// Skip 2 bytes for extensions length
	if offset+2 > len(body) {
		return version, "", ""
	}
	extLen := int(body[offset])<<8 | int(body[offset+1])
	offset += 2
	extEnd := offset + extLen

	if extEnd > len(body) {
		extEnd = len(body)
	}

	for offset+4 <= extEnd {
		extType := int(body[offset])<<8 | int(body[offset+1])
		extDataLen := int(body[offset+2])<<8 | int(body[offset+3])
		extData := body[offset+4:]
		offset += 4 + extDataLen

		switch extType {
		case 0x0000: // SNI
			if len(extData) >= 5 {
				sniLen := int(extData[2])<<8 | int(extData[3])
				if sniLen > 0 && len(extData) >= 5+sniLen {
					sni = string(extData[5 : 5+sniLen])
				}
			}
		case 0x0010: // ALPN
			if len(extData) >= 2 {
				alpnLen := int(extData[0])<<8 | int(extData[1])
				alpnBuf := extData[2:]
				if alpnLen > 0 && len(alpnBuf) >= alpnLen {
					var protos []string
					pos := 0
					for pos < alpnLen {
						plen := int(alpnBuf[pos])
						pos++
						if pos+plen <= len(alpnBuf) {
							protos = append(protos, string(alpnBuf[pos:pos+plen]))
							pos += plen
						}
					}
					alpn = strings.Join(protos, ", ")
				}
			}
		}
	}

	return sni, alpn, version
}

func tlsVersionString(major, minor byte) string {
	switch {
	case major == 3 && minor == 0:
		return "SSL3"
	case major == 3 && minor == 1:
		return "TLS1.0"
	case major == 3 && minor == 2:
		return "TLS1.1"
	case major == 3 && minor == 3:
		return "TLS1.2"
	case major == 3 && minor == 4:
		return "TLS1.3"
	default:
		return fmt.Sprintf("TLS?%d.%d", major, minor)
	}
}

var httpMethods = []string{"GET ", "POST ", "PUT ", "DELETE ", "HEAD ", "OPTIONS ", "CONNECT ", "PATCH ", "TRACE "}

func tryDetectHTTP(payload []byte, info *ProtocolInfo) bool {
	if len(payload) < 4 {
		return false
	}

	for _, method := range httpMethods {
		if len(payload) >= len(method) && bytes.HasPrefix(payload, []byte(method)) {
			info.Protocol = ProtoHTTP
			info.IsHTTP = true
			info.HTTPMethod = strings.TrimSpace(method)

			// Parse path from "GET /path HTTP/1.1"
			parts := bytes.SplitN(payload, []byte(" "), 3)
			if len(parts) >= 2 {
				info.HTTPPath = string(parts[1])
			}

			// Find Host header
			lines := bytes.Split(payload, []byte("\r\n"))
			for _, line := range lines {
				trimmed := bytes.TrimSpace(line)
				if bytes.HasPrefix(trimmed, []byte("Host:")) {
					info.HTTPHost = strings.TrimSpace(string(trimmed[5:]))
					break
				}
			}

			return true
		}
	}

	return false
}

func tryDetectQUIC(payload []byte) bool {
	if len(payload) < 7 {
		return false
	}
	// QUIC version 1: first byte has specific form
	// QUIC Long Header: 0xC0 | (bits)
	// For QUIC v1, check for version field
	if payload[0]&0x80 != 0 {
		if len(payload) >= 5 {
			// Check for known QUIC versions
			ver := binary.BigEndian.Uint32(payload[1:5])
			switch ver {
			case 0x00000001, 0x51303433, 0xbabababa, 0xff000000:
				return true
			}
		}
	}
	return false
}

func extractDNSQuery(payload []byte) string {
	if len(payload) < 12 {
		return ""
	}

	// Skip DNS header (12 bytes), parse question section
	offset := 12
	if offset >= len(payload) {
		return ""
	}

	var labels []string
	for {
		if offset >= len(payload) {
			return ""
		}
		length := int(payload[offset])
		if length == 0 {
			offset++
			break
		}
		if length&0xC0 == 0xC0 {
			// Compression pointer, skip
			offset += 2
			break
		}
		offset++
		if offset+length > len(payload) {
			return ""
		}
		labels = append(labels, string(payload[offset:offset+length]))
		offset += length
	}

	return strings.Join(labels, ".")
}
