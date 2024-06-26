package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	. "github.com/veloceapps/lastpass-go"
)

var _ = Describe("Integration", func() {
	It("creates, reads, updates, deletes account", func() {
		testStart := time.Now().Unix()
		acct := &Account{
			ID:       "",
			Name:     "test site",
			Username: "test user",
			Password: "test pwd",
			URL:      "https://testURL",
			Group:    "test group",
			Notes:    "test notes",
		}

		By("adding")
		Expect(client.Add(context.Background(), acct)).To(Succeed())

		By("updating")
		acct.Username = "updated user"
		acct.Password = "updated pwd"
		Expect(client.Update(context.Background(), acct)).To(Succeed())

		By("reading")
		updated := accountForID(client, acct.ID)
		Expect(updated).To(
			PointTo(MatchAllFields(Fields{
				"ID":              Equal(acct.ID),
				"Name":            Equal(acct.Name),
				"Username":        Equal(acct.Username),
				"Password":        Equal(acct.Password),
				"URL":             Equal(acct.URL),
				"Group":           Equal(acct.Group),
				"Share":           BeEmpty(),
				"Notes":           Equal(acct.Notes),
				"LastModifiedGMT": Not(BeEmpty()),
				"LastTouch":       Not(BeEmpty()),
			})))
		lastModified, err := strconv.ParseUint(updated.LastModifiedGMT, 10, 32)
		Expect(err).ToNot(HaveOccurred())
		Expect(lastModified).To(BeNumerically("~", testStart, 120))

		lastTouch, err := strconv.ParseUint(updated.LastTouch, 10, 32)
		Expect(err).ToNot(HaveOccurred())
		// lastTouch is not in GMT.
		// Expect it to be within 12 hours offset range from GMT.
		Expect(lastTouch).To(BeNumerically("~", testStart, 60*60*12))

		By("deleting")
		Expect(client.Delete(context.Background(), acct)).To(Succeed())
		Expect(accountForID(client, acct.ID)).To(BeNil())
	})

	When("accout does not exist", func() {
		var acct *Account
		const id string = "nonExisting"
		BeforeEach(func() {
			acct = &Account{ID: id}
		})
		Describe("Update()", func() {
			It("returns AccountNotFoundError", func() {
				Expect(client.Update(context.Background(), acct)).To(
					MatchError(&AccountNotFoundError{ID: id}))
			})
		})

		Describe("Delete()", func() {
			It("returns AccountNotFoundError", func() {
				Expect(client.Delete(context.Background(), acct)).To(
					MatchError(&AccountNotFoundError{ID: id}))
			})
		})
	})

	// Prerequisites:
	// Client 2 creates two shared folders and invites client 1
	// 1. LASTPASS_SHARE
	// 2. LASTPASS_SHARE_READ_ONLY with read only permissions
	Context("shared folder", func() {
		It("creates, reads, deletes accounts", func() {
			share := os.Getenv("LASTPASS_SHARE")
			Expect(share).NotTo(BeEmpty())
			acct := &Account{
				Name:     "fake-name",
				Username: "fake-username",
				Password: "fake-password",
				URL:      "http://fake-url",
				Group:    "fake-group",
				Share:    share,
				Notes:    "fake-notes",
			}

			By("client 1 creating")
			Expect(client.Add(context.Background(), acct)).To(Succeed())

			By("client 2 logging in")
			var err error
			client2, err := NewClient(context.Background(), username2, password2)
			Expect(err).NotTo(HaveOccurred())

			By("client 2 reading")
			containSharedAccount := ContainElement(PointTo(MatchAllFields(Fields{
				"ID":              Equal(acct.ID),
				"Name":            Equal(acct.Name),
				"Username":        Equal(acct.Username),
				"Password":        Equal(acct.Password),
				"URL":             Equal(acct.URL),
				"Group":           Equal(acct.Group),
				"Share":           Equal(acct.Share),
				"Notes":           Equal(acct.Notes),
				"LastModifiedGMT": Not(BeEmpty()),
				"LastTouch":       Not(BeEmpty()),
			})))
			Expect(client2.Accounts(context.Background())).To(containSharedAccount)

			By("client 1 deleting")
			Expect(client.Delete(context.Background(), acct)).To(Succeed())

			By("client 2 not reading")
			Expect(client2.Accounts(context.Background())).NotTo(containSharedAccount)

			By("client 2 logging out")
			Expect(client2.Logout(context.Background())).To(Succeed())
		})

		It("fails to add to read-only share", func() {
			shareReadOnly := os.Getenv("LASTPASS_SHARE_READ_ONLY")
			Expect(shareReadOnly).NotTo(BeEmpty())

			acct := &Account{
				Name:  "fake-name",
				Share: shareReadOnly,
			}
			Expect(client.Add(context.Background(), acct)).To(
				MatchError(fmt.Sprintf(
					"Account cannot be written to read-only shared folder %s.", shareReadOnly)))
		})
	})

	Context("offline client", func() {
		It("can buffer and defer HTTP requests", func() {
			cookieJar, err := cookiejar.New(nil)
			Expect(err).NotTo(HaveOccurred())
			onlineHTTPClient := &http.Client{
				Jar: cookieJar,
			}
			onlineClient, err := NewClient(
				context.Background(),
				username2,
				password2,
				WithHTTPClient(onlineHTTPClient),
			)
			Expect(err).NotTo(HaveOccurred())

			acct := &Account{
				ID:       "",
				Name:     "offline test site",
				Username: "offline test user",
				Password: "offline test pwd",
				URL:      "https://offlineTestURL",
			}
			Expect(onlineClient.Add(context.Background(), acct)).To(Succeed())

			encryptedAccounts, err := onlineClient.FetchEncryptedAccounts(context.Background())
			Expect(err).ToNot(HaveOccurred())

			session, err := onlineClient.Session()
			Expect(err).ToNot(HaveOccurred())

			offlineHTTPClient := &offlineHTTPClient{}
			offlineClient, err := NewClientFromSession(
				context.Background(),
				session,
				WithHTTPClient(offlineHTTPClient))
			Expect(err).ToNot(HaveOccurred())

			accounts, err := offlineClient.ParseEncryptedAccounts(bytes.NewReader(encryptedAccounts))
			Expect(err).ToNot(HaveOccurred())

			matchAccount := PointTo(MatchFields(IgnoreExtras, Fields{
				"ID":       Equal(acct.ID),
				"Name":     Equal(acct.Name),
				"Username": Equal(acct.Username),
				"Password": Equal(acct.Password),
				"URL":      Equal(acct.URL),
			}))
			Expect(accounts).To(ContainElement(matchAccount))

			// should buffer the delete request in the offlineHTTPClient
			Expect(offlineClient.Delete(context.Background(), acct)).To(Succeed())
			Expect(offlineHTTPClient.requests).To(HaveLen(1))

			// check that account did not really get deleted
			Expect(accountForID(onlineClient, acct.ID)).To(matchAccount)

			// Let's really delete the account using the buffered request from the offlineHTTPClient.
			httpClient := &http.Client{
				// valid cookie must be set
				Jar: cookieJar,
			}
			resp, err := httpClient.Do(offlineHTTPClient.requests[0])
			Expect(err).ToNot(HaveOccurred())
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))
			Expect(resp).To(HaveHTTPBody(ContainSubstring("msg=\"accountdeleted\"")))
			Expect(accountForID(onlineClient, acct.ID)).To(BeNil())

			Expect(onlineClient.Logout(context.Background())).To(Succeed())
		})
	})
})

type offlineHTTPClient struct {
	requests []*http.Request
}

// handles only lastpass.Client.Delete()
func (c *offlineHTTPClient) Do(req *http.Request) (*http.Response, error) {
	switch req.URL.Path {
	case "/login_check.php":
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(bytes.NewBufferString(
				"<response> <ok accts_version=\"111\"/> </response>")),
		}, nil
	case "/show_website.php":
		c.requests = append(c.requests, req)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(bytes.NewBufferString(
				"<xmlresponse><result aid=\"111\" msg=\"accountdeleted\"></result></xmlresponse>")),
		}, nil
	}
	return nil, fmt.Errorf("unexpected request for path: %s", req.URL.Path)
}

func accountForID(c *Client, accountID string) *Account {
	accounts, err := c.Accounts(context.Background())
	Expect(err).NotTo(HaveOccurred())
	for _, a := range accounts {
		if a.ID == accountID {
			return a
		}
	}
	return nil
}
