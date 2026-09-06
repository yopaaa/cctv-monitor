package onvif

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// TriggerAlarm activates or deactivates the camera's alarm/siren
// via ONVIF Relay Output (SetRelayOutputState).
//
// Pada kamera V380 dan kamera China lainnya, relay output biasanya
// terhubung ke buzzer/siren bawaan kamera.
func TriggerAlarm(deviceURL, username, password string, active bool) error {
	devURL := deriveDeviceURL(deviceURL)

	// Step 1: Get relay output token
	token, err := getRelayOutputToken(devURL, username, password)
	if err != nil {
		return fmt.Errorf("get relay output token: %w", err)
	}

	// Step 2: Set relay state
	state := "inactive"
	if active {
		state = "active"
	}

	if err := setRelayOutputState(devURL, username, password, token, state); err != nil {
		return fmt.Errorf("set relay state to %s: %w", state, err)
	}

	return nil
}

// deriveDeviceURL converts any ONVIF URL to the device service URL.
func deriveDeviceURL(rawURL string) string {
	base := strings.TrimRight(rawURL, "/")
	// Remove known ONVIF service suffixes
	for _, suffix := range []string{
		"/onvif/media_service",
		"/onvif/device_service",
		"/onvif/ptz_service",
		"/onvif/imaging_service",
	} {
		base = strings.TrimSuffix(base, suffix)
	}
	return base + "/onvif/device_service"
}

// --- XML response types ---

type relayOutputsEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		Response struct {
			RelayOutputs []struct {
				Token string `xml:"token,attr"`
			} `xml:"RelayOutputs"`
		} `xml:"GetRelayOutputsResponse"`
	} `xml:"Body"`
}

// getRelayOutputToken retrieves the first relay output token from the camera.
func getRelayOutputToken(deviceURL, username, password string) (string, error) {
	body := `<tds:GetRelayOutputs/>`

	data, err := soapRequest(deviceURL, body, username, password)
	if err != nil {
		return "", err
	}

	var resp relayOutputsEnvelope
	if err := xml.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("parse relay outputs XML: %w", err)
	}

	outputs := resp.Body.Response.RelayOutputs
	if len(outputs) == 0 {
		// Banyak kamera V380/China tidak mengembalikan relay token via GetRelayOutputs
		// tetapi tetap mendukung SetRelayOutputState dengan token default
		return "AlarmRelay", nil
	}

	return outputs[0].Token, nil
}

// setRelayOutputState sets the relay output to active or inactive.
func setRelayOutputState(deviceURL, username, password, token, state string) error {
	body := fmt.Sprintf(`<tds:SetRelayOutputState>
      <tds:RelayOutputToken>%s</tds:RelayOutputToken>
      <tds:LogicalState>%s</tds:LogicalState>
    </tds:SetRelayOutputState>`, token, state)

	_, err := soapRequest(deviceURL, body, username, password)
	return err
}
