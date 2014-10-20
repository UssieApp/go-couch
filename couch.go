// Package couch provides a CouchDB API.
package couch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dustin/httputil"
)

var defaultHdrs = map[string][]string{}

// HTTP Client used by typical requests.
//
// Defaults to http.DefaultClient
var HTTPClient = http.DefaultClient

func createReq(u string) (*http.Request, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}

	if req.URL.User != nil {
		if p, hasp := req.URL.User.Password(); hasp {
			req.SetBasicAuth(req.URL.User.Username(), p)
		}
	}
	return req, nil
}

func unmarshalURL(u string, results interface{}) error {
	req, err := createReq(u)
	if err != nil {
		return err
	}

	r, err := HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	if r.StatusCode != 200 {
		return httputil.HTTPError(r)
	}

	return json.NewDecoder(r.Body).Decode(results)
}

type idAndRev struct {
	ID  string `json:"_id"`
	Rev string `json:"_rev"`
}

// Sends a query to CouchDB and parses the response back.
// method: the name of the HTTP method (POST, PUT,...)
// url: the URL to interact with
// headers: additional headers to pass to the request
// in: body of the request
// out: a structure to fill in with the returned JSON document
func interact(method, u string, headers map[string][]string, in []byte, out interface{}) (int, error) {
	fullHeaders := map[string][]string{}
	for k, v := range headers {
		fullHeaders[k] = v
	}
	if in != nil {
		fullHeaders["Content-Type"] = []string{"application/json"}
	}

	req, err := http.NewRequest(method, u, bytes.NewReader(in))
	if err != nil {
		return 0, err
	}

	req.ContentLength = int64(len(in))
	req.Header = fullHeaders
	req.Close = true

	res, err := HTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return res.StatusCode, httputil.HTTPError(res)
	}
	return res.StatusCode, json.NewDecoder(res.Body).Decode(out)
}

// Database represents operations available on an existing CouchDB
type Database struct {
	Host     string
	Port     string
	Name     string
	authinfo *url.Userinfo

	changesDialer    func(string, string) (net.Conn, error)
	changesFailDelay time.Duration
}

// BaseURL returns the URL to the database server containing this database.
func (p Database) BaseURL() string {
	if p.authinfo == nil {
		return fmt.Sprintf("http://%s:%s", p.Host, p.Port)
	}
	return fmt.Sprintf("http://%s@%s:%s", p.authinfo.String(), p.Host, p.Port)
}

// DBURL returns the URL to this specific database.
func (p Database) DBURL() string {
	return fmt.Sprintf("%s/%s", p.BaseURL(), p.Name)
}

// Running returns true if CouchDB is running (ignores Database.Name)
func (p Database) Running() bool {
	var welcomeJson map[string]interface{}
	u := fmt.Sprintf("%s", p.BaseURL())
	err := unmarshalURL(u, &welcomeJson)
	if err != nil {
		return false
	}
	_, ok := welcomeJson["version"]
	return ok
}

type databaseInfo struct {
	DBName string `json:"db_name"`
	// other stuff too, ignore for now
}

// Exists returns true if this database exists on the CouchDB server
func (p Database) Exists() bool {
	di := &databaseInfo{}
	return unmarshalURL(p.DBURL(), &di) == nil && di.DBName == p.Name
}

func (p Database) simpleOp(method, url string, nokerr error) error {
	ir := Response{}
	if _, err := interact(method, url, defaultHdrs, nil, &ir); err != nil {
		return err
	}
	if !ir.Ok {
		return nokerr
	}
	return nil
}

var (
	errNewDB = errors.New("create database operation returned not-OK")
	errDelDB = errors.New("delete database operation returned not-OK")
)

func (p Database) createDatabase() error {
	return p.simpleOp("PUT", p.DBURL(), errNewDB)
}

// DeleteDatabase deletes the given database and all documents
func (p Database) DeleteDatabase() error {
	return p.simpleOp("DELETE", p.DBURL(), errDelDB)
}

func (p Database) IsAdmin() bool {
	ir := struct{ Admin bool `json:"ADMIN"` }{}
	_, err := interact("GET", fmt.Sprintf("%s/", p.BaseURL()), defaultHdrs, nil, &ir)
	return err != nil && ir.Admin
}

type User struct {
	Name           string      `json:"name,omitempty"`
	Password       string      `json:"password"`
	Email          []string    `json:"email,omitempty"`
	AdminChannels  []string    `json:"admin_channels,omitempty"`
	AdminRoles     []string    `json:"admin_roles"`
	AllChannels    []string    `json:"all_channels,omitempty"`
	Roles          []string    `json:"roles,omitempty"`
	Disabled       bool        `json:"disabled,omitempty"`
}

func (p Database) User(name string) (User, error) {
	u := fmt.Sprintf("%s/_user/%s",  p.DBURL(), name)
	user := User{}
	_, err := interact("GET", u, defaultHdrs, nil, &user)
	return user, err
}

func (p Database) SetUser(user User) error {
	jsonBuf, _ := json.Marshal(user)
	_, err := interact("POST", p.DBURL()+"/_user", defaultHdrs, jsonBuf, nil)
	return err	
}

func (p Database) DeleteUser(name string) error {
	u := fmt.Sprintf("%s/_user/%s",  p.DBURL(), name)
	_, err := interact("DELETE", u, defaultHdrs, nil, nil)
	return err	
}

type Role struct {
	Name           string      `json:"name,omitempty"`
	AdminChannels  []string    `json:"admin_channels"`
	AllChannels    []string    `json:"all_channels,omitempty"`
}

func (p Database) Role(name string) (Role, error) {
	u := fmt.Sprintf("%s/_role/%s",  p.DBURL(), name)
	role := Role{}
	_, err := interact("GET", u, defaultHdrs, nil, &role)
	return role, err
}

func (p Database) SetRole(role Role) error {
	jsonBuf, _ := json.Marshal(role)
	_, err := interact("POST", p.DBURL()+"/_role", defaultHdrs, jsonBuf, nil)
	return err	
}

func (p Database) DeleteRole(name string) error {
	u := fmt.Sprintf("%s/_role/%s",  p.DBURL(), name)
	_, err := interact("DELETE", u, defaultHdrs, nil, nil)
	return err	
}

type session struct {
	SessionId string  `json:"session_id"`
	Expires time.Time `json:"expires"`
	CookieName string `json:"cookie_name"`
}

func (p Database) Session(name string, ttl uint) (http.Cookie, error) {
	user := fmt.Sprintf(`{ "name": "%s", "ttl": %d }`, name, ttl)
	result := session{}
	_, err := interact("POST", p.DBURL()+"/_session", defaultHdrs, []byte(user), &result)
	cookie := http.Cookie{ Name: result.CookieName, Value: result.SessionId, Expires: result.Expires } 
	return cookie, err		
}

var (
	errCompact = errors.New("compact database operation returned not-OK")
	errStartProfile = errors.New("start profile operation returned not-OK")
	errStopProfile = errors.New("stop profile operation returned not-OK")
)

func (p Database) Compact() error {
	return p.simpleOp("GET", p.BaseURL()+"/_compact", errCompact)
}

func (p Database) StartProfile(file string) error {
	req := fmt.Sprintf(`{ "file": "%s" }`, file)
	_, err := interact("POST", p.BaseURL()+"/_profile", defaultHdrs, []byte(req), nil)
	if err != nil {
		return errStartProfile
	}
	return nil
}

func (p Database) StopProfile() error {
	return p.simpleOp("GET", p.BaseURL()+"/_profile", errStopProfile)
}

var errNotRunning = errors.New("couchdb not running")

// Connect to the database at the given URL.
// example:   couch.Connect("http://localhost:5984/testdb/")
func Connect(dburl string) (Database, error) {
	u, err := url.Parse(dburl)
	if err != nil {
		return Database{}, err
	}

	host := u.Host
	port := "80"
	if hp := strings.Split(u.Host, ":"); len(hp) > 1 {
		host = hp[0]
		port = hp[1]
	}

	db := Database{host, port, u.Path[1:], u.User, net.Dial, defaultChangeDelay}
	if !db.Running() {
		return Database{}, errNotRunning
	}
	if !db.Exists() {
		return Database{}, errors.New("database does not exist")
	}

	return db, nil
}

// NewDatabase connects to a CouchDB server and creates the specified
// database if it does not exist.
func NewDatabase(host, port, name string) (Database, error) {
	db := Database{host, port, name, nil, net.Dial, defaultChangeDelay}
	if !db.Running() {
		return db, errNotRunning
	}
	if !db.Exists() {
		if err := db.createDatabase(); err != nil {
			return db, err
		}
	}
	return db, nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// Strip _id and _rev from d, returning them separately if they exist
func cleanJSON(d interface{}) (jsonBuf []byte, id, rev string, err error) {
	jsonBuf, err = json.Marshal(d)
	if err != nil {
		return
	}
	m := map[string]interface{}{}
	must(json.Unmarshal(jsonBuf, &m))
	id, _ = m["_id"].(string)
	delete(m, "_id")
	rev, _ = m["_rev"].(string)
	delete(m, "_rev")
	jsonBuf, err = json.Marshal(m)
	return
}

// Response represents a typical command response from against a CouchDB server.
type Response struct {
	Ok     bool
	ID     string
	Rev    string
	Error  string
	Reason string
}

// Bulk modification interface.
// Each item should be JSON serializable into a valid document.
// "_id" and "_rev" will be honored.
// To delete, add a "_deleted" field with a value of "true" as well
// as a valid "_rev" field.
func (p Database) Bulk(docs []interface{}) (results []Response, err error) {
	m := map[string]interface{}{}
	m["docs"] = docs
	var jsonBuf []byte
	jsonBuf, err = json.Marshal(m)
	if err != nil {
		return
	}

	results = make([]Response, 0, len(docs))
	_, err = interact("POST", p.DBURL()+"/_bulk_docs", defaultHdrs, jsonBuf, &results)
	return
}

// Insert a document into CouchDB, returning id and rev on success.
// Document may specify both "_id" and "_rev" fields (will overwrite existing)
//	or just "_id" (will use that id, but not overwrite existing)
//	or neither (will use autogenerated id)
func (p Database) Insert(d interface{}) (string, string, error) {
	jsonBuf, id, rev, err := cleanJSON(d)
	if err != nil {
		return "", "", err
	}
	if id != "" && rev != "" {
		newRev, err2 := p.Edit(d)
		return id, newRev, err2
	} else if id != "" {
		return p.insertWith(jsonBuf, id)
	} else {
		return p.insert(jsonBuf)
	}
}

// Private implementation of simple autogenerated-id insert
func (p Database) insert(jsonBuf []byte) (string, string, error) {
	ir := Response{}
	if _, err := interact("POST", p.DBURL(), defaultHdrs, jsonBuf, &ir); err != nil {
		return "", "", err
	}
	if !ir.Ok {
		return "", "", fmt.Errorf("%s: %s", ir.Error, ir.Reason)
	}
	return ir.ID, ir.Rev, nil
}

// InsertWith inserts the given document (shouldn't contain "_id" or
// "_rev" tagged fields) using the passed 'id' as the _id. Will fail
// if the id already exists.
func (p Database) InsertWith(d interface{}, id string) (string, string, error) {
	jsonBuf, err := json.Marshal(d)
	if err != nil {
		return "", "", err
	}
	return p.insertWith(jsonBuf, id)
}

// Private implementation of insert with given id
func (p Database) insertWith(jsonBuf []byte, id string) (string, string, error) {
	u := fmt.Sprintf("%s/%s", p.DBURL(), url.QueryEscape(id))
	ir := Response{}
	if _, err := interact("PUT", u, defaultHdrs, jsonBuf, &ir); err != nil {
		return "", "", err
	}
	if !ir.Ok {
		return "", "", fmt.Errorf("%s: %s", ir.Error, ir.Reason)
	}
	return ir.ID, ir.Rev, nil
}

var errNoRev = errors.New("rev not specified in interface (try InsertWith)")

// Edit edits the given document, returning the new revision.
// d must contain "_id" and "_rev" tagged fields.
func (p Database) Edit(d interface{}) (string, error) {
	jsonBuf, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	idRev := idAndRev{}
	must(json.Unmarshal(jsonBuf, &idRev))
	if idRev.ID == "" {
		return "", errNoID
	}
	if idRev.Rev == "" {
		return "", errNoRev
	}
	u := fmt.Sprintf("%s/%s", p.DBURL(), url.QueryEscape(idRev.ID))
	ir := Response{}
	if _, err = interact("PUT", u, defaultHdrs, jsonBuf, &ir); err != nil {
		return "", err
	}
	return ir.Rev, nil
}

// EditWith edits the given document, returning the new revision.
// d should not contain "_id" or "_rev" tagged fields. If it does, they will
// be overwritten with the passed values.
func (p Database) EditWith(d interface{}, id, rev string) (string, error) {
	if id == "" {
		return "", errNoID
	}
	if rev == "" {
		return "", errNoRev
	}
	jsonBuf, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	m := map[string]interface{}{}
	must(json.Unmarshal(jsonBuf, &m))
	m["_id"] = id
	m["_rev"] = rev
	return p.Edit(m)
}

var errNoID = errors.New("no id specified")

// Retrieve unmarshals the document matching id to the given interface
func (p Database) Retrieve(id string, d interface{}) error {
	if id == "" {
		return errNoID
	}

	return unmarshalURL(fmt.Sprintf("%s/%s", p.DBURL(), id), d)
}

// Delete deletes document given by id and rev.
func (p Database) Delete(id, rev string) error {
	headers := map[string][]string{
		"If-Match": []string{rev},
	}
	u := fmt.Sprintf("%s/%s", p.DBURL(), id)
	ir := Response{}
	if _, err := interact("DELETE", u, headers, nil, &ir); err != nil {
		return err
	}
	if !ir.Ok {
		return fmt.Errorf("%s: %s", ir.Error, ir.Reason)
	}
	return nil
}

// DBInfo represents the result from GetInfo
type DBInfo struct {
	Name        string `json:"db_name"`
	DocCount    int64  `json:"doc_count"`
	DocDelCount int64  `json:"doc_del_count"`
	UpdateSeq   int64  `json:"update_seq"`
	PurgeSeq    int64  `json:"purge_seq"`
	Compacting  bool   `json:"compact_running"`
	DiskSize    int64  `json:"disk_size"`
	DataSize    int64  `json:"data_size"`
	StartTime   string `json:"instance_start_time"`
	Version     int    `json:"disk_format_version"`
	CommitedSeq int64  `json:"committed_update_seq"`
}

// GetInfo gets the DBInfo for this database.
func (p Database) GetInfo() (DBInfo, error) {
	rv := DBInfo{}
	err := unmarshalURL(p.DBURL(), &rv)
	return rv, err
}
