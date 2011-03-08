// Copyright 2010 Gary Burd
//
// Licensed under the Apache License, Version 2.0 (the "License"): you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

package web

import (
	"crypto/rand"
	"encoding/hex"
	"os"
)

type filterResponder struct {
	Responder
	filter func(status int, header HeaderMap) (int, HeaderMap)
}

func (rf *filterResponder) Respond(status int, header HeaderMap) ResponseBody {
	return rf.Responder.Respond(rf.filter(status, header))
}

// FilterRespond replaces the request's responder with one that filters the
// arguments to Respond through the supplied filter. This function is intended
// to be used by middleware.
func FilterRespond(req *Request, filter func(status int, header HeaderMap) (int, HeaderMap)) {
	req.Responder = &filterResponder{req.Responder, filter}
}

// SetErrorHandler returns a handler that sets the request's error handler e.
func SetErrorHandler(e ErrorHandler, h Handler) Handler {
	return HandlerFunc(func(req *Request) {
		req.ErrorHandler = e
		h.ServeWeb(req)
	})
}

// Name of XSRF cookie and request parameter.
const (
	XSRFCookieName = "xsrf"
	XSRFParamName  = "xsrf"
)

// ProxyHeaderHandler returns a handler that overrides the Request.RemoteAddr field
// with the value of the header specified by addrName and the
// Request.URL.Scheme field with the value of the header specified by
// schemeName. No fix up is done for a field if the header name equals "" or the
// header is not present.
//
// The header names must be in canonical header name format.
// 
// Here's an example of how to use this handler with Nginx. In the nginx proxy
// configuration, specify a header for the IP address and scheme. The host
// header should also be passed through the proxy:
//
//    location / {
//        proxy_set_header X-Real-IP $remote_addr;
//        proxy_set_header X-Scheme $scheme;
//        proxy_set_header Host $http_host;
//        proxy_pass http://127.0.0.1:8080;
//    }       
//
// In the main function for the application, wrap the application handler with
// the proxy fix up:
//  
//  import (
//      "github.com/garyburd/twister/web"
//      "github.com/garyburd/twister/server"
//  )
//
//  func main() {
//      var h web.Handler
//      ... setup the application handler
//      h = web.ProxyHeaderHandler("X-Scheme", "X-Real-Ip", h)
//	    server.Run(":8080", h)
//  }
func ProxyHeaderHandler(addrName, schemeName string, h Handler) Handler {
	return proxyHeaderHandler{
		addrName:   addrName,
		schemeName: schemeName,
		h:          h,
	}
}

type proxyHeaderHandler struct {
	addrName, schemeName string
	h                    Handler
}

func (h proxyHeaderHandler) ServeWeb(req *Request) {
	if s := req.Header.Get(h.addrName); s != "" {
		req.RemoteAddr = s
	}
	if s := req.Header.Get(h.schemeName); s != "" {
		req.URL.Scheme = s
	}
	h.h.ServeWeb(req)
}

// PorcessForm is deprecated. Use FormHandler.
func ProcessForm(maxRequestBodyLen int, checkXSRF bool, handler Handler) Handler {
	return FormHandler(maxRequestBodyLen, checkXSRF, handler)
}

// FormHandler returns a handler that parses form encoded request bodies.
//
// If the request body is larger than maxRequestBodyLen, then the handler
// responds with an error instead of parsing the request body.
//
// If xsrfCheck is true, then cross-site request forgery protection is enabled.
// The handler rejects POST, PUT, and DELETE requests if the handler does not
// find a matching value for the "xsrf" cookie in the "xsrf" request parameter
// or the X-XSRFToken header. 
//
// The handler ensures that the "xsrf" cookie and the "xsrf" request parameter
// are set before passing the the request to the downstream handler or the
// error handler. The application must include the value fo the "xsrf" request
// parameter in POSTed forms or pass the value to AJAX code so that the
// X-XSRFToken header can be set.
//
// See http://en.wikipedia.org/wiki/Cross-site_request_forgery for information
// on cross-site request forgery.
func FormHandler(maxRequestBodyLen int, checkXSRF bool, h Handler) Handler {
	return formHandler{
		maxRequestBodyLen: maxRequestBodyLen,
		checkXSRF:         checkXSRF,
		h:                 h,
	}
}

type formHandler struct {
	maxRequestBodyLen int
	checkXSRF         bool
	h                 Handler
}

func (h formHandler) ServeWeb(req *Request) {
	if err := req.ParseForm(h.maxRequestBodyLen); err != nil {
		status := StatusBadRequest
		if err == ErrRequestEntityTooLarge {
			status = StatusRequestEntityTooLarge
			if e := req.Header.Get(HeaderExpect); e != "" {
				status = StatusExpectationFailed
			}
		}
		req.Error(status, os.NewError("twister: Error reading or parsing form."))
		return
	}

	if h.checkXSRF {
		const tokenLen = 8
		expectedToken := req.Cookie.Get(XSRFCookieName)

		// Create new XSRF token?
		if len(expectedToken) != tokenLen {
			p := make([]byte, tokenLen/2)
			_, err := rand.Reader.Read(p)
			if err != nil {
				panic("twister: rand read failed")
			}
			expectedToken = hex.EncodeToString(p)
			c := NewCookie(XSRFCookieName, expectedToken).String()
			FilterRespond(req, func(status int, header HeaderMap) (int, HeaderMap) {
				header.Add(HeaderSetCookie, c)
				return status, header
			})
		}

		actualToken := req.Param.Get(XSRFParamName)
		if actualToken == "" {
			actualToken = req.Header.Get(HeaderXXSRFToken)
			req.Param.Set(XSRFParamName, expectedToken)
		}
		if expectedToken != actualToken {
			req.Param.Set(XSRFParamName, expectedToken)
			if req.Method == "POST" ||
				req.Method == "PUT" ||
				req.Method == "DELETE" {
				err := os.NewError("twister: bad xsrf token")
				if actualToken == "" {
					err = os.NewError("twister: missing xsrf token")
				}
				req.Error(StatusNotFound, err)
				return
			}
		}
	}

	h.h.ServeWeb(req)
}
