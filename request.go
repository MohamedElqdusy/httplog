package httplog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// APIAudit struct holds the http request attributes needed
// for auditing an http request
type APIAudit struct {
	RequestID    string        `json:"request_id"`
	TimeStarted  time.Time     `json:"time_started"`
	TimeFinished time.Time     `json:"time_finished"`
	Duration     time.Duration `json:"time_in_millis"`
	ResponseCode int           `json:"response_code"`
	request
	response request
}

type request struct {
	Proto            string `json:"protocol"`
	ProtoMajor       int    `json:"protocol_major"`
	ProtoMinor       int    `json:"protocol_minor"`
	Method           string `json:"request_method"`
	Scheme           string `json:"scheme"`
	Host             string `json:"host"`
	Port             string `json:"port"`
	Path             string `json:"path"`
	RawQuery         string `json:"query"`
	Fragment         string `json:"fragment"`
	Header           string `json:"header"`
	Body             string `json:"body"`
	ContentLength    int64  `json:"content_length"`
	TransferEncoding string `json:"transfer_encoding"`
	Close            bool   `json:"close"`
	Trailer          string `json:"trailer"`
	RemoteAddr       string `json:"remote_address"`
	RequestURI       string `json:"request_uri"`
}

// logReqDispatch determines which, if any, of the logging methods
// you wish to use will be employed
func logReqDispatch(ctx context.Context, log zerolog.Logger, aud *APIAudit, req *http.Request, opts *Opts) error {

	var err error

	err = setRequest(ctx, log, aud, req)
	if err != nil {
		return err
	}

	if opts.HTTPUtil.DumpRequest.Enable {
		requestDump, err := httputil.DumpRequest(req, opts.HTTPUtil.DumpRequest.Body)
		if err != nil {
			log.Error().Err(err).Msg("")
			return err
		}
		fmt.Printf("httputil.DumpRequest output:\n%s", string(requestDump))
		return nil
	}

	if opts.Log2StdOut.Request.Enable {
		err = logReq2Stdout(log, aud, opts)
		if err != nil {
			log.Error().Err(err).Msg("")
			return err
		}
	}

	return nil
}

// setRequest populates several core fields (TimeStarted, ctx and RequestID)
// for the APIAudit struct being passed into the function
func setRequest(ctx context.Context, log zerolog.Logger, aud *APIAudit, req *http.Request) error {

	var (
		scheme string
	)

	// split host and port out for cleaner logging
	host, port, err := net.SplitHostPort(req.Host)
	if err != nil {
		log.Error().Err(err).Msg("")
		return err
	}

	// determine if the request is an HTTPS request
	isHTTPS := req.TLS != nil

	if isHTTPS {
		scheme = "https"
	} else {
		scheme = "http"
	}

	// convert the Header map from the request to a JSON string
	headerJSON, err := convertHeader(log, req.Header)
	if err != nil {
		log.Error().Err(err).Msg("")
		return err
	}

	// convert the Trailer map from the request to a JSON string
	trailerJSON, err := convertHeader(log, req.Trailer)
	if err != nil {
		log.Error().Err(err).Msg("")
		return err
	}

	body, err := dumpBody(req)
	if err != nil {
		log.Error().Err(err).Msg("")
		return err
	}

	aud.RequestID = RequestID(ctx)
	aud.request.Proto = req.Proto
	aud.request.ProtoMajor = req.ProtoMajor
	aud.request.ProtoMinor = req.ProtoMinor
	aud.request.Method = req.Method
	aud.request.Scheme = scheme
	aud.request.Host = host
	aud.request.Port = port
	aud.request.Path = req.URL.Path
	aud.request.RawQuery = req.URL.RawQuery
	aud.request.Fragment = req.URL.Fragment
	aud.request.Body = body
	aud.request.Header = headerJSON
	aud.request.ContentLength = req.ContentLength
	aud.request.TransferEncoding = strings.Join(req.TransferEncoding, ",")
	aud.request.Close = req.Close
	aud.request.Trailer = trailerJSON
	aud.request.RemoteAddr = req.RemoteAddr
	aud.request.RequestURI = req.RequestURI

	return nil
}

// convertHeader returns a JSON string representation of an http.Header map
func convertHeader(log zerolog.Logger, hdr http.Header) (string, error) {

	// convert the http.Header map from the request to a slice of bytes
	jsonBytes, err := json.Marshal(hdr)
	if err != nil {
		log.Error().Err(err).Msg("")
		return "", err
	}

	// convert the slice of bytes to a JSON string
	headerJSON := string(jsonBytes)

	return headerJSON, nil

}

// drainBody reads all of b to memory and then returns two equivalent
// ReadClosers yielding the same bytes.
//
// It returns an error if the initial slurp of all bytes fails. It does not attempt
// to make the returned ReadClosers have identical error-matching behavior.
// Function lifted straight from net/http/httputil package
func drainBody(b io.ReadCloser) (r1, r2 io.ReadCloser, err error) {
	if b == http.NoBody {
		// No copying needed. Preserve the magic sentinel meaning of NoBody.
		return http.NoBody, http.NoBody, nil
	}
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(b); err != nil {
		return nil, b, err
	}
	if err = b.Close(); err != nil {
		return nil, b, err
	}
	return ioutil.NopCloser(&buf), ioutil.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

func dumpBody(req *http.Request) (string, error) {
	var err error
	save := req.Body
	save, req.Body, err = drainBody(req.Body)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer

	chunked := len(req.TransferEncoding) > 0 && req.TransferEncoding[0] == "chunked"

	if req.Body != nil {
		var dest io.Writer = &b
		if chunked {
			dest = httputil.NewChunkedWriter(dest)
		}
		_, err = io.Copy(dest, req.Body)
		if chunked {
			dest.(io.Closer).Close()
			io.WriteString(&b, "\r\n")
		}
	}

	req.Body = save
	if err != nil {
		return "", err
	}
	return string(b.Bytes()), nil
}

// func logFormValues(lgr zerolog.Logger, req *http.Request) (zerolog.Logger, error) {

// 	var i int

// 	err := req.ParseForm()
// 	if err != nil {
// 		return nil, err
// 	}

// 	for key, valSlice := range req.Form {
// 		for _, val := range valSlice {
// 			i++
// 			formValue := fmt.Sprintf("%s: %s", key, val)
// 			lgr = lgr.With().Str(fmt.Sprintf("Form(%d)", i), formValue))
// 		}
// 	}

// 	for key, valSlice := range req.PostForm {
// 		for _, val := range valSlice {
// 			i++
// 			formValue := fmt.Sprintf("%s: %s", key, val)
// 			lgr = lgr.With(Str(fmt.Sprintf("PostForm(%d)", i), formValue))
// 		}
// 	}

// 	return lgr, nil
// }

func logReq2Stdout(log zerolog.Logger, aud *APIAudit, opts *Opts) error {

	// logger, err = logFormValues(logger, req)
	// if err != nil {
	// 	return err
	// }

	// All header key:value pairs written to JSON
	if opts.Log2StdOut.Request.Options.Header {
		log = log.With().Str("header_json", aud.request.Header).Logger()
	}

	if opts.Log2StdOut.Request.Options.Body {
		log = log.With().Str("body", aud.request.Body).Logger()
	}

	log.Info().
		Str("request_id", aud.RequestID).
		Str("method", aud.request.Method).
		// most url.URL components split out
		Str("scheme", aud.request.Scheme).
		Str("host", aud.request.Host).
		Str("port", aud.request.Port).
		Str("path", aud.request.Path).
		// The protocol version for incoming server requests.
		Str("protocol", aud.request.Proto).
		Int("proto_major", aud.request.ProtoMajor).
		Int("proto_minor", aud.request.ProtoMinor).
		Int64("content_length", aud.request.ContentLength).
		Str("transfer_encoding", aud.request.TransferEncoding).
		Bool("close", aud.request.Close).
		Str("remote_Addr", aud.request.RemoteAddr).
		Str("request_URI", aud.request.RequestURI).
		Msg("Request Received")

	return nil
}
