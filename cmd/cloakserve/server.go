package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

type server struct {
	pool *chromePool
	port int
}

// connParams holds the parsed query-string connection configuration.
type connParams struct {
	seed      string
	timezone  string
	locale    string
	proxy     string
	geoip     bool
	extraArgs []string
}

var specialParams = map[string]bool{
	"fingerprint": true, "proxy": true, "geoip": true, "locale": true, "timezone": true,
}

func parseConnParams(r *http.Request) connParams {
	q := r.URL.Query()
	p := connParams{}
	for key, vals := range q {
		if len(vals) == 0 {
			continue
		}
		val := vals[0]
		switch key {
		case "fingerprint":
			p.seed = val
		case "timezone":
			p.timezone = val
		case "locale":
			p.locale = val
		case "proxy":
			p.proxy = val
		case "geoip":
			lv := strings.ToLower(val)
			p.geoip = lv == "true" || lv == "1" || lv == "yes"
		default:
			if !specialParams[key] {
				p.extraArgs = append(p.extraArgs, fmt.Sprintf("--fingerprint-%s=%s", key, val))
			}
		}
	}
	return p
}

func (s *server) externalHost(r *http.Request) string {
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		if h := strings.TrimSpace(strings.SplitN(fh, ",", 2)[0]); h != "" {
			return h
		}
	}
	if r.Host != "" {
		return r.Host
	}
	return fmt.Sprintf("localhost:%d", s.port)
}

func (s *server) wsScheme(r *http.Request) string {
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
	}
	proto = strings.ToLower(strings.TrimSpace(strings.SplitN(proto, ",", 2)[0]))
	if proto == "https" {
		return "wss"
	}
	return "ws"
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, s.pool.status())
}

func (s *server) handleJSONVersion(w http.ResponseWriter, r *http.Request) {
	params := parseConnParams(r)
	cp, err := s.pool.getOrLaunch(params.seed, params.extraArgs, params.timezone, params.locale, params.proxy, params.geoip)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	body, err := fetchCDP(r.Context(), cp.cdpPort, "/json/version")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "CDP endpoint unreachable"})
		return
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "bad CDP response"})
		return
	}

	host := s.externalHost(r)
	scheme := s.wsScheme(r)
	wsPath := "devtools/browser"
	if params.seed != "" {
		wsPath = fmt.Sprintf("fingerprint/%s/devtools/browser", params.seed)
	}
	guid := ""
	if orig, _ := data["webSocketDebuggerUrl"].(string); strings.Contains(orig, "/devtools/") {
		parts := strings.Split(orig, "/")
		guid = parts[len(parts)-1]
	}
	data["webSocketDebuggerUrl"] = fmt.Sprintf("%s://%s/%s/%s", scheme, host, wsPath, guid)
	writeJSON(w, http.StatusOK, data)
}

func (s *server) handleJSONList(w http.ResponseWriter, r *http.Request) {
	params := parseConnParams(r)
	cp, err := s.pool.getOrLaunch(params.seed, params.extraArgs, params.timezone, params.locale, params.proxy, params.geoip)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	body, err := fetchCDP(r.Context(), cp.cdpPort, "/json/list")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "CDP endpoint unreachable"})
		return
	}
	var entries []map[string]any
	if err := json.Unmarshal(body, &entries); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "bad CDP response"})
		return
	}
	host := s.externalHost(r)
	scheme := s.wsScheme(r)
	for _, e := range entries {
		orig, ok := e["webSocketDebuggerUrl"].(string)
		if !ok {
			continue
		}
		tail := orig
		if i := strings.Index(orig, "/devtools/"); i >= 0 {
			tail = orig[i+len("/devtools/"):]
		}
		if params.seed != "" {
			e["webSocketDebuggerUrl"] = fmt.Sprintf("%s://%s/fingerprint/%s/devtools/%s", scheme, host, params.seed, tail)
		} else {
			e["webSocketDebuggerUrl"] = fmt.Sprintf("%s://%s/devtools/%s", scheme, host, tail)
		}
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleWSDefault proxies /devtools/{type}/{guid} to the default Chrome.
func (s *server) handleWSDefault(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/devtools/")
	cp, err := s.pool.getOrLaunch("", nil, "", "", "", false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	target := fmt.Sprintf("ws://127.0.0.1:%d/devtools/%s", cp.cdpPort, path)
	s.proxyWS(w, r, "__default__", target, fmt.Sprintf("CDP default [%s]", path))
}

// handleWSSeed proxies /fingerprint/{seed}/devtools/{type}/{guid}.
func (s *server) handleWSSeed(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/fingerprint/")
	slash := strings.Index(rest, "/")
	if slash < 0 {
		http.NotFound(w, r)
		return
	}
	seed := rest[:slash]
	remainder := rest[slash+1:] // "devtools/{type}/{guid}"
	path := strings.TrimPrefix(remainder, "devtools/")

	cp, err := s.pool.getOrLaunch(seed, nil, "", "", "", false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	target := fmt.Sprintf("ws://127.0.0.1:%d/devtools/%s", cp.cdpPort, path)
	s.proxyWS(w, r, seed, target, fmt.Sprintf("CDP seed=%s [%s]", seed, path))
}

// proxyWS bridges the client WebSocket to the Chrome CDP WebSocket.
func (s *server) proxyWS(w http.ResponseWriter, r *http.Request, seedKey, targetURL, label string) {
	clientWS, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:     []string{"*"},
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("%s: accept failed: %v", label, err)
		return
	}
	clientWS.SetReadLimit(256 * 1024 * 1024)

	ctx := context.Background()
	cdpWS, _, err := websocket.Dial(ctx, targetURL, &websocket.DialOptions{})
	if err != nil {
		log.Printf("%s: dial CDP failed: %v", label, err)
		clientWS.Close(websocket.StatusInternalError, "cdp dial failed")
		return
	}
	cdpWS.SetReadLimit(256 * 1024 * 1024)

	s.pool.connect(seedKey)
	defer s.pool.disconnect(seedKey)
	log.Printf("%s: connected", label)

	proxyCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	pump := func(src, dst *websocket.Conn) {
		defer cancel()
		for {
			typ, data, err := src.Read(proxyCtx)
			if err != nil {
				return
			}
			if err := dst.Write(proxyCtx, typ, data); err != nil {
				return
			}
		}
	}
	go pump(clientWS, cdpWS)
	go pump(cdpWS, clientWS)

	<-proxyCtx.Done()
	clientWS.Close(websocket.StatusNormalClosure, "")
	cdpWS.Close(websocket.StatusNormalClosure, "")
	log.Printf("%s: disconnected", label)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
