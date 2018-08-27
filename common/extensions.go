package common

import (
	"net/url"
	"strings"

	"github.com/Azure/azure-storage-file-go/2017-07-29/azfile"
)

/////////////////////////////////////////////////////////////////////////////////////////////////
type URLStringExtension string

func (s URLStringExtension) RedactSigQueryParamForLogging() string {
	u, err := url.Parse(string(s))
	if err != nil {
		return string(s)
	}
	return URLExtension{URL: *u}.RedactSigQueryParamForLogging()
}

/////////////////////////////////////////////////////////////////////////////////////////////////
type URLExtension struct {
	url.URL
}

func (u URLExtension) RedactSigQueryParamForLogging() string {
	if ok, rawQuery := redactSigQueryParam(u.RawQuery); ok {
		u.RawQuery = rawQuery
	}

	return u.String()
}

func redactSigQueryParam(rawQuery string) (bool, string) {
	rawQuery = strings.ToLower(rawQuery) // lowercase the string so we can look for ?sig= and &sig=
	sigFound := strings.Contains(rawQuery, "?sig=")
	if !sigFound {
		sigFound = strings.Contains(rawQuery, "&sig=")
		if !sigFound {
			return sigFound, rawQuery // [?|&]sig= not found; return same rawQuery passed in (no memory allocation)
		}
	}
	// [?|&]sig= found, redact its value
	values, _ := url.ParseQuery(rawQuery)
	for name := range values {
		if strings.EqualFold(name, "sig") {
			values[name] = []string{"REDACTED"}
		}
	}
	return sigFound, values.Encode()
}

/////////////////////////////////////////////////////////////////////////////////////////////////
type FileURLPartsExtension struct {
	azfile.FileURLParts
}

func (parts FileURLPartsExtension) GetShareURL() url.URL {
	parts.DirectoryOrFilePath = ""
	return parts.URL()
}

func (parts FileURLPartsExtension) GetServiceURL() url.URL {
	parts.ShareName = ""
	parts.DirectoryOrFilePath = ""
	return parts.URL()
}
