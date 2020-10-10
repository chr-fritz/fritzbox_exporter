// Query UPNP variables from Fritz!Box devices.
package fritzbox_upnp

// Copyright 2016 Nils Decker
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// curl http://fritz.box:49000/igddesc.xml
// curl http://fritz.box:49000/any.xml
// curl http://fritz.box:49000/igdconnSCPD.xml
// curl http://fritz.box:49000/igdicfgSCPD.xml
// curl http://fritz.box:49000/igddslSCPD.xml
// curl http://fritz.box:49000/igd2ipv6fwcSCPD.xml

const textXml = `text/xml; charset="utf-8"`

var ErrInvalidSOAPResponse = errors.New("invalid SOAP response")

// Root of the UPNP tree
type Root struct {
	BaseUrl  string
	Username string
	Password string
	Device   Device              `xml:"device"`
	Services map[string]*Service // Map of all services indexed by .ServiceType
}

// An UPNP Device
type Device struct {
	root *Root

	DeviceType       string `xml:"deviceType"`
	FriendlyName     string `xml:"friendlyName"`
	Manufacturer     string `xml:"manufacturer"`
	ManufacturerUrl  string `xml:"manufacturerURL"`
	ModelDescription string `xml:"modelDescription"`
	ModelName        string `xml:"modelName"`
	ModelNumber      string `xml:"modelNumber"`
	ModelUrl         string `xml:"modelURL"`
	UDN              string `xml:"UDN"`

	Services []*Service `xml:"serviceList>service"` // Service of the device
	Devices  []*Device  `xml:"deviceList>device"`   // Sub-Devices of the device

	PresentationUrl string `xml:"presentationURL"`
}

// An UPNP Service
type Service struct {
	Device *Device

	ServiceType string `xml:"serviceType"`
	ServiceId   string `xml:"serviceId"`
	ControlUrl  string `xml:"controlURL"`
	EventSubUrl string `xml:"eventSubURL"`
	SCPDUrl     string `xml:"SCPDURL"`

	Actions        map[string]*Action // All actions available on the service
	StateVariables []*StateVariable   // All state variables available on the service
}

type scpdRoot struct {
	Actions        []*Action        `xml:"actionList>action"`
	StateVariables []*StateVariable `xml:"serviceStateTable>stateVariable"`
}

// An UPNP Acton on a service
type Action struct {
	service *Service

	Name        string               `xml:"name"`
	Arguments   []*Argument          `xml:"argumentList>argument"`
	ArgumentMap map[string]*Argument // Map of arguments indexed by .Name
}

// An Inüut Argument to pass to an action
type ActionArgument struct {
	Name  string
	Value interface{}
}

// structs to unmarshal SOAP faults
type SoapEnvelope struct {
	XMLName xml.Name `xml:"http://schemas.xmlsoap.org/soap/envelope/ Envelope"`
	Body    SoapBody
}
type SoapBody struct {
	XMLName xml.Name `xml:"http://schemas.xmlsoap.org/soap/envelope/ Body"`
	Fault   SoapFault
}
type SoapFault struct {
	XMLName     xml.Name    `xml:"http://schemas.xmlsoap.org/soap/envelope/ Fault"`
	FaultCode   string      `xml:"faultcode"`
	FaultString string      `xml:"faultstring"`
	Detail      FaultDetail `xml:"detail"`
}
type FaultDetail struct {
	UpnpError UpnpError `xml:"UPnPError"`
}
type UpnpError struct {
	ErrorCode        int    `xml:"errorCode"`
	ErrorDescription string `xml:"errorDescription"`
}

// Returns if the action seems to be a query for information.
// This is determined by checking if the action has no input arguments and at least one output argument.
func (a *Action) IsGetOnly() bool {
	for _, a := range a.Arguments {
		if a.Direction == "in" {
			return false
		}
	}
	return len(a.Arguments) > 0
}

// An Argument to an action
type Argument struct {
	Name                 string `xml:"name"`
	Direction            string `xml:"direction"`
	RelatedStateVariable string `xml:"relatedStateVariable"`
	StateVariable        *StateVariable
}

// A state variable that can be manipulated through actions
type StateVariable struct {
	Name         string `xml:"name"`
	DataType     string `xml:"dataType"`
	DefaultValue string `xml:"defaultValue"`
}

// The result of a Call() contains all output arguments of the call.
// The map is indexed by the name of the state variable.
// The type of the value is string, uint64 or bool depending of the DataType of the variable.
type Result map[string]interface{}

// load the whole tree
func (r *Root) load() error {
	igddesc, err := http.Get(
		fmt.Sprintf("%s/igddesc.xml", r.BaseUrl),
	)

	if err != nil {
		return err
	}

	defer igddesc.Body.Close()

	dec := xml.NewDecoder(igddesc.Body)

	err = dec.Decode(r)
	if err != nil {
		return err
	}

	r.Services = make(map[string]*Service)
	return r.Device.fillServices(r)
}

func (r *Root) loadTr64() error {
	igddesc, err := http.Get(
		fmt.Sprintf("%s/tr64desc.xml", r.BaseUrl),
	)

	if err != nil {
		return err
	}

	defer igddesc.Body.Close()

	dec := xml.NewDecoder(igddesc.Body)

	err = dec.Decode(r)
	if err != nil {
		return err
	}

	r.Services = make(map[string]*Service)
	return r.Device.fillServices(r)
}

// load all service descriptions
func (d *Device) fillServices(r *Root) error {
	d.root = r

	for _, s := range d.Services {
		s.Device = d

		response, err := http.Get(r.BaseUrl + s.SCPDUrl)
		if err != nil {
			return err
		}

		defer response.Body.Close()

		var scpd scpdRoot

		dec := xml.NewDecoder(response.Body)
		err = dec.Decode(&scpd)
		if err != nil {
			return err
		}

		s.Actions = make(map[string]*Action)
		for _, a := range scpd.Actions {
			s.Actions[a.Name] = a
		}
		s.StateVariables = scpd.StateVariables

		for _, a := range s.Actions {
			a.service = s
			a.ArgumentMap = make(map[string]*Argument)

			for _, arg := range a.Arguments {
				for _, svar := range s.StateVariables {
					if arg.RelatedStateVariable == svar.Name {
						arg.StateVariable = svar
					}
				}

				a.ArgumentMap[arg.Name] = arg
			}
		}

		r.Services[s.ServiceType] = s
	}
	for _, d2 := range d.Devices {
		err := d2.fillServices(r)
		if err != nil {
			return err
		}
	}
	return nil
}

const SoapActionXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
	`<s:Body><u:%s xmlns:u=%s>%s</u:%s xmlns:u=%s></s:Body>` +
	`</s:Envelope>`

const SoapActionParamXML = `<%s>%s</%s>`

func (a *Action) createCallHttpRequest(actionArg *ActionArgument) (*http.Request, error) {
	argsString := ""
	if actionArg != nil {
		var buf bytes.Buffer
		sValue := fmt.Sprintf("%v", actionArg.Value)
		xml.EscapeText(&buf, []byte(sValue))
		argsString += fmt.Sprintf(SoapActionParamXML, actionArg.Name, buf.String(), actionArg.Name)
	}
	bodystr := fmt.Sprintf(SoapActionXML, a.Name, a.service.ServiceType, argsString, a.Name, a.service.ServiceType)

	url := a.service.Device.root.BaseUrl + a.service.ControlUrl
	body := strings.NewReader(bodystr)

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}

	action := fmt.Sprintf("%s#%s", a.service.ServiceType, a.Name)

	req.Header.Set("Content-Type", textXml)
	req.Header.Set("SOAPAction", action)

	return req, nil
}

// store auth header for reuse
var authHeader = ""

// Call an action with argument if given
func (a *Action) Call(actionArg *ActionArgument) (Result, error) {
	req, err := a.createCallHttpRequest(actionArg)

	if err != nil {
		return nil, err
	}

	// reuse prior authHeader, to avoid unnecessary authentication
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	// first try call without auth header
	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, err
	}

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close() // close now, since we make a new request below or fail

		if wwwAuth != "" && a.service.Device.root.Username != "" && a.service.Device.root.Password != "" {
			// call failed, but we have a password so calculate header and try again
			authHeader, err = a.getDigestAuthHeader(wwwAuth, a.service.Device.root.Username, a.service.Device.root.Password)
			if err != nil {
				return nil, errors.New(fmt.Sprintf("%s: %s", a.Name, err.Error))
			}

			req, err = a.createCallHttpRequest(actionArg)
			if err != nil {
				return nil, errors.New(fmt.Sprintf("%s: %s", a.Name, err.Error))
			}

			req.Header.Set("Authorization", authHeader)

			resp, err = http.DefaultClient.Do(req)

			if err != nil {
				return nil, errors.New(fmt.Sprintf("%s: %s", a.Name, err.Error))
			}

		} else {
			return nil, errors.New(fmt.Sprintf("%s: Unauthorized, but no username and password given", a.Name))
		}
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("%s (%d)", http.StatusText(resp.StatusCode), resp.StatusCode)
		if resp.StatusCode == 500 {
			buf := new(strings.Builder)
			io.Copy(buf, resp.Body)
			body := buf.String()
			//fmt.Println(body)

			var soapEnv SoapEnvelope
			err := xml.Unmarshal([]byte(body), &soapEnv)
			if err != nil {
				errMsg = fmt.Sprintf("error decoding SOAPFault: %s", err.Error())
			} else {
				soapFault := soapEnv.Body.Fault

				if soapFault.FaultString == "UPnPError" {
					upe := soapFault.Detail.UpnpError

					errMsg = fmt.Sprintf("SAOPFault: %s %d (%s)", soapFault.FaultString, upe.ErrorCode, upe.ErrorDescription)
				} else {
					errMsg = fmt.Sprintf("SAOPFault: %s", soapFault.FaultString)
				}
			}
		}
		return nil, errors.New(fmt.Sprintf("%s: %s", a.Name, errMsg))
	}

	return a.parseSoapResponse(resp.Body)
}

func (a *Action) getDigestAuthHeader(wwwAuth string, username string, password string) (string, error) {
	// parse www-auth header
	if !strings.HasPrefix(wwwAuth, "Digest ") {
		return "", errors.New(fmt.Sprintf("WWW-Authentication header is not Digest: '%s'", wwwAuth))
	}

	s := wwwAuth[7:]
	d := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		d[strings.Trim(parts[0], "\" ")] = strings.Trim(parts[1], "\" ")
	}

	if d["algorithm"] == "" {
		d["algorithm"] = "MD5"
	} else if d["algorithm"] != "MD5" {
		return "", errors.New(fmt.Sprintf("digest algorithm not supported: %s != MD5", d["algorithm"]))
	}

	if d["qop"] != "auth" {
		return "", errors.New(fmt.Sprintf("digest qop not supported: %s != auth", d["qop"]))
	}

	// calc h1 and h2
	ha1 := fmt.Sprintf("%x", md5.Sum([]byte(username+":"+d["realm"]+":"+password)))

	ha2 := fmt.Sprintf("%x", md5.Sum([]byte("POST:"+a.service.ControlUrl)))

	cn := make([]byte, 8)
	rand.Read(cn)
	cnonce := fmt.Sprintf("%x", cn)

	nCounter := 1
	nc := fmt.Sprintf("%08x", nCounter)

	ds := strings.Join([]string{ha1, d["nonce"], nc, cnonce, d["qop"], ha2}, ":")
	response := fmt.Sprintf("%x", md5.Sum([]byte(ds)))

	authHeader := fmt.Sprintf("Digest username=\"%s\", realm=\"%s\", nonce=\"%s\", uri=\"%s\", cnonce=\"%s\", nc=%s, qop=%s, response=\"%s\", algorithm=%s",
		username, d["realm"], d["nonce"], a.service.ControlUrl, cnonce, nc, d["qop"], response, d["algorithm"])

	return authHeader, nil
}

func (a *Action) parseSoapResponse(r io.Reader) (Result, error) {
	res := make(Result)
	dec := xml.NewDecoder(r)

	for {
		t, err := dec.Token()
		if err == io.EOF {
			return res, nil
		}

		if err != nil {
			return nil, err
		}

		if se, ok := t.(xml.StartElement); ok {
			arg, ok := a.ArgumentMap[se.Name.Local]

			if ok {
				t2, err := dec.Token()
				if err != nil {
					return nil, err
				}

				var val string
				switch element := t2.(type) {
				case xml.EndElement:
					val = ""
				case xml.CharData:
					val = string(element)
				default:
					return nil, ErrInvalidSOAPResponse
				}

				converted, err := convertResult(val, arg)
				if err != nil {
					return nil, err
				}
				res[arg.StateVariable.Name] = converted
			}
		}

	}
}

func convertResult(val string, arg *Argument) (interface{}, error) {
	switch arg.StateVariable.DataType {
	case "string":
		return val, nil
	case "boolean":
		return bool(val == "1"), nil

	case "ui1", "ui2", "ui4":
		// type ui4 can contain values greater than 2^32!
		res, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			return nil, err
		}
		return res, nil
	case "i4":
		res, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, err
		}
		return int64(res), nil
	case "dateTime", "uuid":
		// data types we don't convert yet
		return val, nil
	default:
		return nil, fmt.Errorf("unknown datatype: %s (%s)", arg.StateVariable.DataType, val)
	}
}

// Load the services tree from an device.
func LoadServices(baseurl string, username string, password string) (*Root, error) {

	if strings.HasPrefix(baseurl, "https://") {
		// disable certificate validation, since fritz.box uses self signed cert
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	var root = &Root{
		BaseUrl:  baseurl,
		Username: username,
		Password: password,
	}

	err := root.load()
	if err != nil {
		return nil, err
	}

	var rootTr64 = &Root{
		BaseUrl:  baseurl,
		Username: username,
		Password: password,
	}

	err = rootTr64.loadTr64()
	if err != nil {
		return nil, err
	}

	for k, v := range rootTr64.Services {
		root.Services[k] = v
	}

	return root, nil
}
