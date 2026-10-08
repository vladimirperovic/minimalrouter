package dnsfilter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

func DecodeMetadata(data []byte, value any) error {
	if len(data) > MaxMetadataBytes {
		return errors.New("DNS filter metadata is too large")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing DNS filter metadata")
	}
	return nil
}

type Client struct{ SocketPath string }

func NewClient() *Client { return &Client{SocketPath: SocketPath} }

// Domains are streamed through a bounded socket, never accepted as paths or
// dnsmasq directives. Emit is called only for an apply operation.
func (c *Client) Call(ctx context.Context, request Request, emit func(io.Writer) error) (Applied, error) {
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return Applied{}, errors.New("DNS filter helper unavailable")
	}
	defer conn.Close()
	deadline := time.Now().Add(90 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return Applied{}, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	writer := bufio.NewWriterSize(conn, 32768)
	request.Version = 1
	if err = json.NewEncoder(writer).Encode(request); err != nil {
		return Applied{}, err
	}
	if emit != nil {
		if err = emit(writer); err != nil {
			return Applied{}, err
		}
	}
	if err = writer.Flush(); err != nil {
		return Applied{}, err
	}
	if unix, ok := conn.(*net.UnixConn); ok {
		if err = unix.CloseWrite(); err != nil {
			return Applied{}, err
		}
	}
	raw, err := io.ReadAll(io.LimitReader(conn, MaxMetadataBytes+1))
	if err != nil {
		return Applied{}, err
	}
	var response Response
	if err = DecodeMetadata(raw, &response); err != nil {
		return Applied{}, err
	}
	if response.Error != "" {
		return response.State, errors.New(response.Error)
	}
	return response.State, nil
}
