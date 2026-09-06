package onvif

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GetStreamURI discovers the RTSP stream URI from an ONVIF camera.
// It performs two SOAP calls:
//  1. GetProfiles — to retrieve the first media profile token.
//  2. GetStreamUri — to get the RTSP URL for that profile.
//
// If username is provided, WS-Security UsernameToken (PasswordDigest) is used.
func GetStreamURI(deviceURL, username, password string) (string, error) {
	mediaURL := deriveMediaURL(deviceURL)

	// Step 1: Get first profile token
	profileToken, err := getFirstProfileToken(mediaURL, username, password)
	if err != nil {
		return "", fmt.Errorf("get profiles: %w", err)
	}

	// Step 2: Get RTSP stream URI
	uri, err := getStreamURI(mediaURL, username, password, profileToken)
	if err != nil {
		return "", fmt.Errorf("get stream uri: %w", err)
	}

	// Inject credentials into RTSP URL if not already present
	if username != "" && !strings.Contains(uri, "@") {
		uri = injectCredentials(uri, username, password)
	}

	return uri, nil
}

// deriveMediaURL converts a device service URL to the media service URL.
func deriveMediaURL(deviceURL string) string {
	if strings.Contains(deviceURL, "media") {
		return deviceURL
	}
	base := strings.TrimRight(deviceURL, "/")
	base = strings.TrimSuffix(base, "/onvif/device_service")
	return base + "/onvif/media_service"
}

// generateWSSecurityHeader creates a WS-Security UsernameToken header
// with PasswordDigest authentication.
//
// PasswordDigest = Base64(SHA1(Nonce + Created + Password))
func generateWSSecurityHeader(username, password string) string {
	// Generate 16 random bytes for nonce
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		// Fallback to timestamp-based nonce (should never happen)
		nonce = []byte(fmt.Sprintf("%016d", time.Now().UnixNano()))
	}
	nonceB64 := base64.StdEncoding.EncodeToString(nonce)

	// ISO 8601 timestamp
	created := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// PasswordDigest = Base64(SHA1(Nonce + Created + Password))
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(password))
	digestB64 := base64.StdEncoding.EncodeToString(h.Sum(nil))

	return fmt.Sprintf(`<Security s:mustUnderstand="1"
      xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">
      <UsernameToken>
        <Username>%s</Username>
        <Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">%s</Password>
        <Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">%s</Nonce>
        <Created xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">%s</Created>
      </UsernameToken>
    </Security>`, username, digestB64, nonceB64, created)
}

// soapRequest sends a SOAP request to the given URL and returns the response body.
func soapRequest(url, body, username, password string) ([]byte, error) {
	secHeader := ""
	if username != "" {
		secHeader = generateWSSecurityHeader(username, password)
	}

	envelope := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:trt="http://www.onvif.org/ver10/media/wsdl"
            xmlns:tds="http://www.onvif.org/ver10/device/wsdl"
            xmlns:tt="http://www.onvif.org/ver10/schema">
  <s:Header>%s</s:Header>
  <s:Body>%s</s:Body>
</s:Envelope>`, secHeader, body)

	req, err := http.NewRequest("POST", url, strings.NewReader(envelope))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("soap request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("soap error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// --- XML response types for ONVIF SOAP parsing ---

type profilesEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		Response struct {
			Profiles []struct {
				Token string `xml:"token,attr"`
				Name  string `xml:"Name"`
			} `xml:"Profiles"`
		} `xml:"GetProfilesResponse"`
	} `xml:"Body"`
}

type streamURIEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		Response struct {
			MediaUri struct {
				URI string `xml:"Uri"`
			} `xml:"MediaUri"`
		} `xml:"GetStreamUriResponse"`
	} `xml:"Body"`
}

// getFirstProfileToken retrieves the first media profile token from the camera.
func getFirstProfileToken(mediaURL, username, password string) (string, error) {
	body := `<trt:GetProfiles/>`

	data, err := soapRequest(mediaURL, body, username, password)
	if err != nil {
		return "", err
	}

	var resp profilesEnvelope
	if err := xml.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("parse profiles XML: %w (response: %.200s)", err, string(data))
	}

	profiles := resp.Body.Response.Profiles
	if len(profiles) == 0 {
		return "", fmt.Errorf("no media profiles found on camera")
	}

	return profiles[0].Token, nil
}

// getStreamURI retrieves the RTSP stream URI for a given profile token.
func getStreamURI(mediaURL, username, password, profileToken string) (string, error) {
	body := fmt.Sprintf(`<trt:GetStreamUri>
      <trt:StreamSetup>
        <tt:Stream>RTP-Unicast</tt:Stream>
        <tt:Transport>
          <tt:Protocol>RTSP</tt:Protocol>
        </tt:Transport>
      </trt:StreamSetup>
      <trt:ProfileToken>%s</trt:ProfileToken>
    </trt:GetStreamUri>`, profileToken)

	data, err := soapRequest(mediaURL, body, username, password)
	if err != nil {
		return "", err
	}

	var resp streamURIEnvelope
	if err := xml.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("parse stream URI XML: %w (response: %.200s)", err, string(data))
	}

	uri := resp.Body.Response.MediaUri.URI
	if uri == "" {
		return "", fmt.Errorf("empty RTSP URI in ONVIF response")
	}

	return uri, nil
}

// injectCredentials inserts username:password into an RTSP URL.
// Example: rtsp://192.168.1.100:554/stream → rtsp://admin:pass@192.168.1.100:554/stream
func injectCredentials(rtspURL, username, password string) string {
	if strings.HasPrefix(rtspURL, "rtsp://") {
		return fmt.Sprintf("rtsp://%s:%s@%s", username, password, strings.TrimPrefix(rtspURL, "rtsp://"))
	}
	return rtspURL
}
