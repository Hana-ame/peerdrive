package echproxy

// IwaraHeaders returns the standard iwara request headers (Origin / X-Site /
// Referer) that the iwara API expects. IwaraClient sends them explicitly on
// every request so the API accepts the call regardless of the egress path.
func IwaraHeaders(host string) map[string]string {
	if host == "" {
		host = DefaultIWARAHost
	}
	return map[string]string{
		"Origin":  "https://" + host,
		"X-Site":  host,
		"Referer": "https://" + host + "/",
	}
}
