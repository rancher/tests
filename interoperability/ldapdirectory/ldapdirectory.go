package ldapdirectory

import (
	"fmt"
	"net"
	"strings"

	"github.com/go-ldap/ldap/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
)

const (
	userSurname    = "user"
	ouObjectClass  = "organizationalUnit"
	userClass      = "inetOrgPerson"
	groupClass     = "groupOfNames"
	anyObjectClass = "(objectClass=*)"
)

// Dial connects to the configured OpenLDAP server as its service account and closes the connection on session cleanup
func Dial(ts *session.Session, client *rancher.Client) (*ldap.Conn, error) {
	cfg := client.Auth.OLDAP.Config
	server := authactions.AuthProviderServer(cfg.Hostname, cfg.IP)

	conn, err := ldap.DialURL(fmt.Sprintf("ldap://%s:%d", server, authactions.AuthProviderPort), ldap.DialWithDialer(&net.Dialer{Timeout: defaults.TenSecondTimeout}))
	if err != nil {
		return nil, fmt.Errorf("connecting to OpenLDAP server %s: %w", server, err)
	}

	conn.SetTimeout(defaults.OneMinuteTimeout)

	if err := conn.Bind(cfg.ServiceAccount.DistinguishedName, cfg.ServiceAccount.Password); err != nil {
		conn.Close()
		return nil, fmt.Errorf("binding to OpenLDAP server %s as the service account: %w", server, err)
	}

	ts.RegisterCleanupFunc(func() error {
		conn.Close()
		return nil
	})

	return conn, nil
}

// CreateOU creates an organizational unit under the parent DN and deletes it on session cleanup
func CreateOU(ts *session.Session, conn *ldap.Conn, parentDN, name string) (string, error) {
	dn := fmt.Sprintf("ou=%s,%s", name, parentDN)

	request := ldap.NewAddRequest(dn, nil)
	request.Attribute("objectClass", []string{ouObjectClass})
	request.Attribute("ou", []string{name})

	if err := conn.Add(request); err != nil {
		return "", fmt.Errorf("creating OpenLDAP organizational unit %s: %w", dn, err)
	}

	ts.RegisterCleanupFunc(func() error { return DeleteEntry(conn, dn) })

	return dn, nil
}

// CreateUser creates an inetOrgPerson entry with the given uid and password under the parent DN and deletes it on session cleanup
func CreateUser(ts *session.Session, conn *ldap.Conn, parentDN, uid, password string) (string, error) {
	dn := fmt.Sprintf("cn=%s,%s", uid, parentDN)

	request := ldap.NewAddRequest(dn, nil)
	request.Attribute("objectClass", []string{userClass})
	request.Attribute("cn", []string{uid})
	request.Attribute("sn", []string{userSurname})
	request.Attribute("uid", []string{uid})
	request.Attribute("userPassword", []string{password})

	if err := conn.Add(request); err != nil {
		return "", fmt.Errorf("creating OpenLDAP user %s: %w", dn, err)
	}

	ts.RegisterCleanupFunc(func() error { return DeleteEntry(conn, dn) })

	return dn, nil
}

// CreateGroup creates a groupOfNames entry with the given member DNs under the parent DN and deletes it on session cleanup
func CreateGroup(ts *session.Session, conn *ldap.Conn, parentDN, cn string, memberDNs []string) (string, error) {
	dn := fmt.Sprintf("cn=%s,%s", cn, parentDN)

	request := ldap.NewAddRequest(dn, nil)
	request.Attribute("objectClass", []string{groupClass})
	request.Attribute("cn", []string{cn})
	request.Attribute("member", memberDNs)

	if err := conn.Add(request); err != nil {
		return "", fmt.Errorf("creating OpenLDAP group %s: %w", dn, err)
	}

	ts.RegisterCleanupFunc(func() error { return DeleteEntry(conn, dn) })

	return dn, nil
}

// MoveEntry moves an entry under a new parent DN, returns its new DN and deletes it from there on session cleanup
func MoveEntry(ts *session.Session, conn *ldap.Conn, dn, newParentDN string) (string, error) {
	rdn := strings.SplitN(dn, ",", 2)[0]
	newDN := fmt.Sprintf("%s,%s", rdn, newParentDN)

	if err := conn.ModifyDN(ldap.NewModifyDNRequest(dn, rdn, true, newParentDN)); err != nil {
		return "", fmt.Errorf("moving OpenLDAP entry %s under %s: %w", dn, newParentDN, err)
	}

	ts.RegisterCleanupFunc(func() error { return DeleteEntry(conn, newDN) })

	return newDN, nil
}

// FindAttribute returns the single value of an attribute on the entry with the given DN
func FindAttribute(conn *ldap.Conn, dn, attribute string) (string, error) {
	result, err := conn.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 0, false, anyObjectClass, []string{attribute}, nil))
	if err != nil {
		return "", fmt.Errorf("reading %s of OpenLDAP entry %s: %w", attribute, dn, err)
	}

	if len(result.Entries) != 1 {
		return "", fmt.Errorf("expected one OpenLDAP entry at %s, got %d", dn, len(result.Entries))
	}

	value := result.Entries[0].GetAttributeValue(attribute)
	if value == "" {
		return "", fmt.Errorf("OpenLDAP entry %s has no %s", dn, attribute)
	}

	return value, nil
}

// RemoveAttribute deletes every value of an attribute from the entry with the given DN
func RemoveAttribute(conn *ldap.Conn, dn, attribute string) error {
	request := ldap.NewModifyRequest(dn, nil)
	request.Delete(attribute, nil)

	if err := conn.Modify(request); err != nil {
		return fmt.Errorf("removing %s from OpenLDAP entry %s: %w", attribute, dn, err)
	}

	return nil
}

// DeleteEntry deletes the entry with the given DN, treating an entry that is already gone as deleted
func DeleteEntry(conn *ldap.Conn, dn string) error {
	err := conn.Del(ldap.NewDelRequest(dn, nil))
	if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
		return nil
	}

	return err
}
