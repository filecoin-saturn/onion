package onion

import (
	"fmt"
	"net/url"
	"strings"
)

var kuboGWHost = "ipfs.io"

type URLBuilder struct {
	lassieIP  string
	l1ShimIP  string
	l1NginxIP string
	bifrostIP string
}

func NewURLBuilder(lassieIP, l1ShimIP, l1NginxIP, bifrostIP string) *URLBuilder {
	return &URLBuilder{
		lassieIP:  lassieIP,
		l1ShimIP:  l1ShimIP,
		l1NginxIP: l1NginxIP,
		bifrostIP: bifrostIP,
	}
}

func (ub *URLBuilder) BuildURLsToTest(bifrostReqUrl string) URLsToTest {

	return URLsToTest{
		Path:       parseRequestPath(bifrostReqUrl),
		Lassie:     ub.BuildLassieUrl(bifrostReqUrl),
		L1Shim:     ub.BuildL1ShimUrl(bifrostReqUrl),
		L1Nginx:    ub.BuildL1NginxUrl(bifrostReqUrl),
		KuboGWUrl:  ub.BuildKuboGWUrl(bifrostReqUrl),
		BifrostURL: ub.BuildBifrostUrl(bifrostReqUrl),
	}
}

func (b *URLBuilder) BuildLassieUrl(bifrostUrl string) string {
	u := replaceIPInURL(bifrostUrl, b.lassieIP)
	u = switchHTTPStoHTTP(u)
	return u
}

func (b *URLBuilder) BuildL1ShimUrl(bifrostUrl string) string {
	u := replaceIPInURL(bifrostUrl, b.l1ShimIP)
	u = switchHTTPStoHTTP(u)
	u = u + "&nocache=1"
	return u
}

func (b *URLBuilder) BuildL1NginxUrl(bifrostUrl string) string {
	u := replaceIPInURL(bifrostUrl, b.l1NginxIP)
	u = switchHTTPtoHTTPS(u)
	return u
}

func (b *URLBuilder) BuildKuboGWUrl(bifrostUrl string) string {
	var result string
	if idx := strings.Index(bifrostUrl, "?"); idx != -1 {
		result = bifrostUrl[:idx]
	} else {
		panic("params not found in bifrost url")
	}

	result = replaceIPInURL(result, kuboGWHost)
	return result
}

func (b *URLBuilder) BuildBifrostUrl(bifrostUrl string) string {
	var result string
	if idx := strings.Index(bifrostUrl, "?"); idx != -1 {
		result = bifrostUrl[:idx]
	} else {
		panic("params not found in bifrost url")
	}

	result = replaceIPInURL(result, b.bifrostIP)
	result = switchHTTPStoHTTP(result)
	return result
}

func switchHTTPStoHTTP(u string) string {
	return strings.Replace(u, "https://", "http://", -1)
}

func switchHTTPtoHTTPS(u string) string {
	return strings.Replace(u, "http://", "https://", -1)
}

func replaceIPInURL(s, newIP string) string {
	u, err := url.Parse(s)
	if err != nil {
		panic(fmt.Errorf("failed to parse url: %s", err))
	}
	u.Host = newIP
	return u.String()
}

func parseRequestPath(bifrostUrl string) string {
	u, err := url.Parse(bifrostUrl)
	if err != nil {
		panic(fmt.Errorf("failed to parse bifrost url: %s", err))
	}

	if len(u.Path) == 0 {
		panic(fmt.Errorf("invalid bifrost url: %s; no path", bifrostUrl))
	}
	return u.Path
}

func ParseCidFromPath(path string) string {
	split := strings.SplitN(path, "/ipfs/", 2)
	if len(split) == 2 {
		x := split[1]
		split = strings.SplitN(x, "/", -1)
		return split[0]
	}

	panic("invalid path")
}
