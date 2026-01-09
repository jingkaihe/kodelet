package anthropic

import (
	"bytes"
	"io"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const subscriptionToolNamePrefix = "oc_"

// toolNamePrefixMiddleware creates middleware that prefixes tool names in requests.
// This is required for Anthropic subscription accounts which have specific tool naming requirements.
func toolNamePrefixMiddleware() option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if req.Body == nil {
			return next(req)
		}

		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return next(req)
		}

		// Prefix tool names in the tools array
		body = prefixToolNames(body)

		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))

		return next(req)
	}
}

// prefixToolNames adds the subscription prefix to all tool names in the request body
func prefixToolNames(body []byte) []byte {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return body
	}

	var err error
	for i, tool := range tools.Array() {
		name := tool.Get("name").String()
		if name != "" {
			body, err = sjson.SetBytes(body, "tools."+itoa(i)+".name", subscriptionToolNamePrefix+name)
			if err != nil {
				return body
			}
		}
	}

	return body
}

// itoa converts an int to a string without importing strconv
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b) - 1
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		b[n] = byte('0' + i%10)
		i /= 10
		n--
	}
	if neg {
		b[n] = '-'
		n--
	}
	return string(b[n+1:])
}
