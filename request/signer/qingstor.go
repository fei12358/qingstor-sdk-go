// +-------------------------------------------------------------------------
// | Copyright (C) 2016 Yunify, Inc.
// +-------------------------------------------------------------------------
// | Licensed under the Apache License, Version 2.0 (the "License");
// | you may not use this work except in compliance with the License.
// | You may obtain a copy of the License in the LICENSE file, or at:
// |
// | http://www.apache.org/licenses/LICENSE-2.0
// |
// | Unless required by applicable law or agreed to in writing, software
// | distributed under the License is distributed on an "AS IS" BASIS,
// | WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// | See the License for the specific language governing permissions and
// | limitations under the License.
// +-------------------------------------------------------------------------

package signer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/pengsrc/go-shared/convert"
	"go.uber.org/zap"

	"github.com/qingstor/qingstor-sdk-go/v4/log"
	"github.com/qingstor/qingstor-sdk-go/v4/request/errors"
	"github.com/qingstor/qingstor-sdk-go/v4/utils"
)

// QingStorSigner is the http request signer for QingStor service.
type QingStorSigner struct {
	AccessKeyID            string
	SecretAccessKey        string
	EnableVirtualHostStyle bool
}

// WriteSignature calculates signature and write it to http request header.
func (qss *QingStorSigner) WriteSignature(request *http.Request) error {
	authorization, err := qss.BuildSignature(request)
	if err != nil {
		return err
	}

	request.Header.Set("Authorization", authorization)

	return nil
}

// WriteQuerySignature calculates signature and write it to http request url.
func (qss *QingStorSigner) WriteQuerySignature(request *http.Request, expires int) error {
	query, err := qss.BuildQuerySignature(request, expires)
	if err != nil {
		return err
	}

	if request.URL.RawQuery != "" {
		query = "?" + request.URL.RawQuery + "&" + query
	} else {
		query = "?" + query
	}

	newRequest, err := http.NewRequest(request.Method,
		request.URL.Scheme+"://"+request.URL.Host+utils.URLQueryEscape(request.URL.Path)+query, nil)
	if err != nil {
		return errors.NewSDKError(
			errors.WithAction("new http request in WriteQuerySignature"),
			errors.WithError(err),
		)
	}
	request.URL = newRequest.URL

	return nil
}

// BuildSignature calculates the signature string.
func (qss *QingStorSigner) BuildSignature(request *http.Request) (string, error) {
	logger := log.FromContext(request.Context())
	stringToSign, err := qss.BuildStringToSign(request)
	if err != nil {
		return "", err
	}

	h := hmac.New(sha256.New, []byte(qss.SecretAccessKey))
	h.Write([]byte(stringToSign))

	signature := strings.TrimSpace(base64.StdEncoding.EncodeToString(h.Sum(nil)))
	authorization := "QS " + qss.AccessKeyID + ":" + signature

	logger.Debug("build signature",
		zap.String("qs_authorization", authorization),
		zap.Int64("date", convert.StringToTimestamp(request.Header.Get("Date"), convert.RFC822)),
	)

	return authorization, nil
}

// BuildQuerySignature calculates the signature string for query.
func (qss *QingStorSigner) BuildQuerySignature(request *http.Request, expires int) (string, error) {
	logger := log.FromContext(request.Context())
	stringToSign, err := qss.BuildQueryStringToSign(request, expires)
	if err != nil {
		return "", err
	}

	h := hmac.New(sha256.New, []byte(qss.SecretAccessKey))
	h.Write([]byte(stringToSign))

	signature := strings.TrimSpace(base64.StdEncoding.EncodeToString(h.Sum(nil)))
	signature = utils.URLQueryEscape(signature)
	query := fmt.Sprintf(
		"access_key_id=%s&expires=%d&signature=%s",
		qss.AccessKeyID, expires, signature,
	)

	logger.Debug("build query signature",
		zap.String("signature", query),
		zap.Int64("date", convert.StringToTimestamp(request.Header.Get("Date"), convert.RFC822)),
	)

	return query, nil
}

// BuildStringToSign build the string to sign.
func (qss *QingStorSigner) BuildStringToSign(request *http.Request) (string, error) {
	logger := log.FromContext(request.Context())
	date := request.Header.Get("Date")
	if request.Header.Get("X-QS-Date") != "" {
		date = ""
	}
	stringToSign := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n",
		request.Method,
		request.Header.Get("Content-MD5"),
		request.Header.Get("Content-Type"),
		date,
	)

	stringToSign += qss.buildCanonicalizedHeaders(request)
	canonicalizedResource, err := qss.buildCanonicalizedResource(request)
	if err != nil {
		return "", err
	}
	stringToSign += canonicalizedResource

	logger.Debug("build string to sign",
		zap.String("string", stringToSign),
		zap.Int64("date", convert.StringToTimestamp(request.Header.Get("Date"), convert.RFC822)),
	)

	return stringToSign, nil
}

// BuildQueryStringToSign build the string to sign for query.
func (qss *QingStorSigner) BuildQueryStringToSign(request *http.Request, expires int) (string, error) {
	logger := log.FromContext(request.Context())
	stringToSign := fmt.Sprintf(
		"%s\n%s\n%s\n%d\n",
		request.Method,
		request.Header.Get("Content-MD5"),
		request.Header.Get("Content-Type"),
		expires,
	)

	stringToSign += qss.buildCanonicalizedHeaders(request)
	canonicalizedResource, err := qss.buildCanonicalizedResource(request)
	if err != nil {
		return "", err
	}
	stringToSign += canonicalizedResource

	logger.Debug("build query string to sign",
		zap.String("string", stringToSign),
		zap.Int64("date", convert.StringToTimestamp(request.Header.Get("Date"), convert.RFC822)),
	)

	return stringToSign, nil
}

func (qss *QingStorSigner) buildCanonicalizedHeaders(request *http.Request) string {
	keys := []string{}
	for key := range request.Header {
		if strings.HasPrefix(strings.ToLower(key), "x-qs-") {
			keys = append(keys, strings.TrimSpace(strings.ToLower(key)))
		}
	}

	sort.Strings(keys)

	canonicalizedHeaders := ""
	for _, key := range keys {
		canonicalizedHeaders += key + ":" + strings.TrimSpace(request.Header.Get(key)) + "\n"
	}

	return canonicalizedHeaders
}

func (qss *QingStorSigner) buildCanonicalizedResource(request *http.Request) (string, error) {
	logger := log.FromContext(request.Context())
	path := utils.URLQueryEscape(request.URL.Path)
	query := request.URL.Query()

	if qss.EnableVirtualHostStyle {
		if path == "" {
			path = "/"
		}
		_parts := strings.Split(request.Host, ".")
		path = "/" + _parts[0] + path
	}

	keys := []string{}
	for key := range query {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	parts := []string{}
	for _, key := range keys {
		values := query[key]
		if qss.queryToSign(key) {
			if len(values) > 0 {
				if values[0] != "" {
					value := strings.TrimSpace(strings.Join(values, ""))
					parts = append(parts, key+"="+value)
				} else {
					parts = append(parts, key)
				}
			} else {
				parts = append(parts, key)
			}
		}
	}

	joinedParts := strings.Join(parts, "&")
	if joinedParts != "" {
		path = path + "?" + joinedParts
	}

	logger.Debug("canonicalized resource",
		zap.String("path", path),
		zap.Int64("date", convert.StringToTimestamp(request.Header.Get("Date"), convert.RFC822)),
	)

	return path, nil
}

func (qss *QingStorSigner) queryToSign(key string) bool {
	keysMap := map[string]bool{
		"acl":                          true,
		"append":                       true,
		"cname":                        true,
		"cors":                         true,
		"delete":                       true,
		"image":                        true,
		"lifecycle":                    true,
		"logging":                      true,
		"mirror":                       true,
		"notification":                 true,
		"part_number":                  true,
		"policy":                       true,
		"position":                     true,
		"replication":                  true,
		"stats":                        true,
		"upload_id":                    true,
		"uploads":                      true,
		"versioning":                   true,
		"version_id":                   true,
		"versions":                     true,
		"response-expires":             true,
		"response-cache-control":       true,
		"response-content-type":        true,
		"response-content-language":    true,
		"response-content-encoding":    true,
		"response-content-disposition": true,
	}

	return keysMap[key]
}
