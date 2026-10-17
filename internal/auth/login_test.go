package auth

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestParseLoginPageUnescapesJSONPublicKey(t *testing.T) {
	page, err := ParseLoginPage([]byte(`<input name="flowId" value="flow-123"><script>var data={"publicKey":"abc123\/xyz=="}</script>`))
	if err != nil {
		t.Fatal(err)
	}
	if page.FlowID != "flow-123" {
		t.Fatalf("FlowID = %q", page.FlowID)
	}
	if page.PublicKey != "abc123/xyz==" {
		t.Fatalf("PublicKey = %q", page.PublicKey)
	}
}

func TestHTTPSServiceCallback(t *testing.T) {
	target, err := url.Parse("https://my.muc.edu.cn/user/simpleSSOLogin?ticket=redacted")
	if err != nil {
		t.Fatal(err)
	}
	if !isHTTPSServiceCallback(target) {
		t.Fatal("HTTPS service callback was not recognized")
	}
	for _, rawURL := range []string{
		"http://my.muc.edu.cn/user/simpleSSOLogin?ticket=redacted",
		"https://my.muc.edu.cn/page/11",
		"https://example.edu/user/simpleSSOLogin",
	} {
		target, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		if isHTTPSServiceCallback(target) {
			t.Fatalf("unexpected service callback match: %s", rawURL)
		}
	}
}

func TestRequiredPortalCookiesSelectsStableMinimum(t *testing.T) {
	cookies, err := requiredPortalCookies([]*http.Cookie{
		{Name: "SSO_TGC", Value: "cas"},
		{Name: "nginx", Value: "route"},
		{Name: "JSESSIONID", Value: "session"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 2 || cookies[0].Name != "JSESSIONID" || cookies[1].Name != "nginx" {
		t.Fatalf("cookies = %#v", cookies)
	}

	_, err = requiredPortalCookies([]*http.Cookie{{Name: "JSESSIONID", Value: "session"}})
	if err == nil || !strings.Contains(err.Error(), "nginx") {
		t.Fatalf("err = %v", err)
	}
}
