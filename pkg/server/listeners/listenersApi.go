/**
 * Copyright 2025 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package listeners

import (
	"crypto/x509"
	"fmt"
	"goto/pkg/events"
	"goto/pkg/server/middleware"
	gototls "goto/pkg/tls"
	"goto/pkg/util"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("listeners", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	lRouter := util.PathRouter(r, "/?server?/listeners")
	util.AddRoute(lRouter, "/add", addListener, "POST", "PUT")
	util.AddRoute(lRouter, "/update", updateListener, "POST", "PUT")
	util.AddRoute(lRouter, "/{port}/cert/auto/{domain}", autoCert, "PUT", "POST")
	util.AddRoute(lRouter, "/{port}/cert/autosni", autoSNI, "PUT", "POST")
	util.AddRoute(lRouter, "/{port}/cert/add", addListenerCert, "PUT", "POST")
	util.AddRoute(lRouter, "/{port}/key/add", addListenerKey, "PUT", "POST")
	util.AddRoute(lRouter, "/{port}/cert/remove", removeListenerCertAndKey, "PUT", "POST")
	util.AddRoute(lRouter, "/{port}/cert", getListenerCertOrKey, "GET")
	util.AddRoute(lRouter, "/{port}/key", getListenerCertOrKey, "GET")
	util.AddRoute(lRouter, "/{port}/ca/add", addListenerCACert, "PUT", "POST")
	util.AddRoute(lRouter, "/{port}/ca/clear", clearListenerCACerts, "PUT", "POST")
	util.AddRoute(lRouter, "/{port}/remove", removeListener, "PUT", "POST")
	util.AddRoute(lRouter, "/{port}/open", openListener, "PUT", "POST")
	util.AddRoute(lRouter, "/{port}/reopen", openListener, "PUT", "POST")
	util.AddRoute(lRouter, "/{port}/close", closeListener, "PUT", "POST")
	util.AddRoute(lRouter, "/{port}?", getListeners, "GET")
}

func addListener(w http.ResponseWriter, r *http.Request) {
	addOrUpdateListenerAndRespond(w, r)
}

func updateListener(w http.ResponseWriter, r *http.Request) {
	addOrUpdateListenerAndRespond(w, r)
}

func addListenerCert(w http.ResponseWriter, r *http.Request) {
	addListenerCertOrKey(w, r, true)
}

func addListenerKey(w http.ResponseWriter, r *http.Request) {
	addListenerCertOrKey(w, r, false)
}

func removeListenerCertAndKey(w http.ResponseWriter, r *http.Request) {
	if l := validateListener(w, r); l != nil {
		msg := ""
		l.lock.Lock()
		l.RawKey = nil
		l.RawCert = nil
		l.Cert = nil
		l.TLS = false
		l.AutoCert = false
		l.CommonName = ""
		l.lock.Unlock()
		if l.reopenListener() {
			msg = fmt.Sprintf("Cert and Key removed for listener %d, and reopened\n", l.Port)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			msg = fmt.Sprintf("Cert and Key removed for listener %d but failed to reopen\n", l.Port)
		}
		events.SendRequestEvent("Listener Cert Removed", msg, r)
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
	}
}

func addListenerCACert(w http.ResponseWriter, r *http.Request) {
	if l := validateListener(w, r); l != nil {
		msg := ""
		data := util.ReadBytes(r.Body)
		if len(data) > 0 {
			l.lock.Lock()
			if l.CACerts == nil {
				l.CACerts = x509.NewCertPool()
			}
			l.CACerts.AppendCertsFromPEM(data)
			l.lock.Unlock()
			events.SendRequestEvent("Listener CA Cert Added", msg, r)
			if l.reopenListener() {
				msg = fmt.Sprintf("CA Cert added for listener %d, and reopened\n", l.Port)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				msg = fmt.Sprintf("CA Cert added for listener %d but failed to reopen\n", l.Port)
			}

		} else {
			w.WriteHeader(http.StatusBadRequest)
			msg = "No payload"
		}
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
	}
}

func clearListenerCACerts(w http.ResponseWriter, r *http.Request) {
	if l := validateListener(w, r); l != nil {
		msg := ""
		l.lock.Lock()
		l.CACerts = x509.NewCertPool()
		l.lock.Unlock()
		if l.reopenListener() {
			msg = fmt.Sprintf("CA Certs cleared for listener %d, and reopened\n", l.Port)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			msg = fmt.Sprintf("CA Certs cleared for listener %d but failed to reopen\n", l.Port)
		}
		events.SendRequestEvent("Listener CA Certs Cleared", msg, r)
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
	}
}

func getListenerCertOrKey(w http.ResponseWriter, r *http.Request) {
	cert := strings.Contains(r.RequestURI, "cert")
	key := strings.Contains(r.RequestURI, "key")
	if l := validateListener(w, r); l != nil {
		msg := ""
		var err error
		if cert {
			raw := l.RawCert
			if raw == nil {
				raw, err = gototls.EncodeX509Cert(l.Cert)
			}
			if raw != nil {
				w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Write(raw)
				msg = "Listener TLS cert served"
			} else if err != nil {
				msg = fmt.Sprintf("Failed to serve listener tls cert with error: %s", err.Error())
			} else {
				msg = "Failed to serve listener tls cert"
			}
		} else if key {
			raw := l.RawKey
			if raw == nil {
				raw, err = gototls.EncodeX509Key(l.Cert)
			}
			if raw != nil {
				w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Write(raw)
				msg = "Listener TLS key served"
			} else if err != nil {
				msg = fmt.Sprintf("Failed to serve listener tls key with error: %s", err.Error())
			} else {
				msg = "Failed to serve listener tls key"
			}
		} else {
			w.WriteHeader(http.StatusBadRequest)
			msg = "Neither cert nor key requested"
			fmt.Fprintln(w, msg)
		}
		util.AddLogMessage(msg, r)
	}
}

func autoCert(w http.ResponseWriter, r *http.Request) {
	if l := validateListener(w, r); l != nil {
		msg := ""
		if domain := util.GetStringParamValue(r, "domain"); domain != "" {
			if cert, err := gototls.CreateCertificate(domain, fmt.Sprintf("%s-%d", l.Label, l.Port)); err == nil {
				l.Cert = cert
				if l.reopenListener() {
					msg = fmt.Sprintf("Cert auto-generated for listener %d\n", l.Port)
					events.SendRequestEvent("Listener Cert Generated", msg, r)
				} else {
					msg = fmt.Sprintf("Failed to reopen listener %d for auto-generate cert\n", l.Port)
				}
			} else {
				msg = fmt.Sprintf("Failed to auto-generate cert for listener %d\n", l.Port)
				w.WriteHeader(http.StatusInternalServerError)
			}
		} else {
			msg = fmt.Sprintf("Missing domain for cert auto-generation for listener %d\n", l.Port)
			w.WriteHeader(http.StatusBadRequest)
		}
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
	}
}

func autoSNI(w http.ResponseWriter, r *http.Request) {
	if l := validateListener(w, r); l != nil {
		l.AutoSNI = true
		l.AutoCert = false
		msg := ""
		if l.reopenListener() {
			msg = fmt.Sprintf("Listener [%d] configured to auto-generate cert for any SNI", l.Port)
		} else {
			msg = fmt.Sprintf("Failed to reopen listener %d for auto-generate SNI\n", l.Port)
		}
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
	}
}

func getListeners(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	listenersLock.RLock()
	defer listenersLock.RUnlock()
	if port > 0 {
		util.WriteJsonPayload(w, GetListenerForPort(port))
	} else {
		util.WriteJsonPayload(w, GetListeners())
	}
}

func openListener(w http.ResponseWriter, r *http.Request) {
	if l := validateListener(w, r); l != nil {
		msg := ""
		if l.Listener == nil {
			if l.openListener(true) {
				if l.TLS {
					msg = fmt.Sprintf("TLS Listener opened on port %d\n", l.Port)
				} else {
					msg = fmt.Sprintf("Listener opened on port %d\n", l.Port)
				}
				events.SendRequestEventJSON("Listener Opened", l.ListenerID,
					map[string]interface{}{"listener": l, "status": msg}, r)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				msg = fmt.Sprintf("Failed to listen on port %d\n", l.Port)
			}
		} else {
			l.reopenListener()
			if l.TLS {
				msg = fmt.Sprintf("TLS Listener reopened on port %d\n", l.Port)
			} else {
				msg = fmt.Sprintf("Listener reopened on port %d\n", l.Port)
			}
			events.SendRequestEventJSON("Listener Reopened", l.ListenerID,
				map[string]interface{}{"listener": l, "status": msg}, r)
		}
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
	}
}

func closeListener(w http.ResponseWriter, r *http.Request) {
	if l := validateListener(w, r); l != nil {
		msg := ""
		if l.Listener == nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("Port %d not open\n", l.Port)
		} else {
			l.closeListener()
			msg = fmt.Sprintf("Listener on port %d closed\n", l.Port)
			events.SendRequestEvent("Listener Closed", msg, r)
		}
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
	}
}

func removeListener(w http.ResponseWriter, r *http.Request) {
	if l := validateListener(w, r); l != nil {
		l.lock.Lock()
		if l.Listener != nil {
			l.Listener.Close()
			l.Listener = nil
		}
		l.lock.Unlock()
		listenersLock.Lock()
		if l.IsGRPC {
			delete(grpcListeners, l.Port)
		} else if l.IsUDP {
			delete(udpListeners, l.Port)
		}
		delete(listeners, l.Port)
		listenersLock.Unlock()
		msg := fmt.Sprintf("Listener on port %d removed", l.Port)
		events.SendRequestEvent("Listener Removed", msg, r)
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
	}
}
