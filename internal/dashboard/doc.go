// Package dashboard serves the registry's browser UI: the JSON API the
// embedded SPA calls, and the HTML handlers those routes replaced. The HTML
// handlers stay for their tests; the live mux mounts NewHTTPAPI instead.
package dashboard
