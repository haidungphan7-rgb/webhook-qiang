// Package healthz is the single "is the service answering" probe. The tray
// icon colour, the status report and doctor all ask the same question; one
// implementation keeps the answers from drifting apart.
package healthz

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// timeout bounds one probe. Two seconds: long enough for a busy machine to
// answer a loopback GET, short enough that a poll tick never stacks up.
const timeout = 2 * time.Second

// Check reports whether the service on the port answers /healthz with 200.
// Blue means the port answers - nothing else, per the tray contract.
func Check(port uint16) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/healthz", port), nil)
	if err != nil {
		return false
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}

	defer res.Body.Close()

	return res.StatusCode == http.StatusOK
}
