// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// bootstrapScript is injected into the served index.html <head>, before any app
// JS runs. It mirrors the init scripts of linux/main.go:
//   - seeds the single-user session token into localStorage, and
//   - routes target=_blank link clicks to the system browser via the bound
//     App.OpenInBrowser method (window.go.main.App is provided by Wails).
func bootstrapScript(sessionToken string) string {
	return fmt.Sprintf(`<script>
(function () {
  try { localStorage.setItem('focalboardSessionId', %q); } catch (e) {}
  document.addEventListener('click', function (e) {
    var a = e.target.closest && e.target.closest('a[target="_blank"]');
    if (a && window.go && window.go.main && window.go.main.App) {
      e.preventDefault();
      window.go.main.App.OpenInBrowser(a.getAttribute('href'));
    }
  });
})();
</script>`, sessionToken)
}

// newServerProxy builds a reverse proxy to the in-process Focalboard server.
// Wails serves the window from its own origin and delegates every request to
// this handler, so HTTP and the /ws WebSocket upgrade all reach localhost:port.
// The bootstrap script is injected into HTML responses on the way back.
func newServerProxy(port int, sessionToken string) (http.Handler, error) {
	target, err := url.Parse(fmt.Sprintf("http://localhost:%d", port))
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		req.Host = target.Host
		// Ask upstream for uncompressed HTML so we can inject the bootstrap script.
		req.Header.Del("Accept-Encoding")
	}

	inject := []byte(bootstrapScript(sessionToken))
	proxy.ModifyResponse = func(resp *http.Response) error {
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			return nil
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		// Insert right after <head> so the token is set before app scripts load.
		if idx := bytes.Index(body, []byte("<head>")); idx != -1 {
			at := idx + len("<head>")
			body = append(body[:at], append(inject, body[at:]...)...)
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
		return nil
	}

	return proxy, nil
}
