package ontap

import "net/http"

// Do sends a request through the client's guarded transport.
func Do(c *Client, r *http.Request) error {
	res, err := c.http.Do(r)
	if err == nil {
		res.Body.Close()
	}
	return err
}
