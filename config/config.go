// mauIRC-server - The IRC bouncer/backend system for mauIRC clients.
// Copyright (C) 2016 Tulir Asokan

// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

// Package config contains configurations
package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"time"

	yaml "gopkg.in/yaml.v2"

	"maunium.net/go/mauirc-server/common/messages"
	"maunium.net/go/mauirc-server/config/mail"
	"maunium.net/go/mauirc-server/interfaces"
	"maunium.net/go/maulogger"
)

// MessageBufferSize is a magical value
const MessageBufferSize = 128

var log = maulogger.CreateSublogger("Net", maulogger.LevelInfo)

// NewConfig creates a new Configuration instance
func NewConfig(path string) interfaces.Configuration {
	path, _ = filepath.Abs(path)
	return &configImpl{Path: path}
}

type configImpl struct {
	Path             string               `yaml:"-" json:"-"`
	SQL              mysqlImpl            `yaml:"sql" json:"sql"`
	Users            userListImpl         `yaml:"users" json:"users"`
	Mail             mail.Config          `yaml:"mail" json:"mail"`
	IP               string               `yaml:"ip" json:"ip"`
	Port             int                  `yaml:"port" json:"port"`
	TrustHeadersF    bool                 `yaml:"trust-headers" json:"trust-headers"`
	AutosaveConfig   bool                 `yaml:"save-config-on-edit" json:"save-config-on-edit"`
	Address          string               `yaml:"external-address" json:"external-address"`
	CSecretB64       string               `yaml:"cookie-secret" json:"cookie-secret"`
	HTTPSOnlyCookies bool                 `yaml:"https-only" json:"https-only"`
	Ident            interfaces.IdentConf `yaml:"ident" json:"ident"`
	CookieSecret     []byte               `yaml:"-" json:"-"`
}

type mysqlImpl struct {
	IP       string `yaml:"ip" json:"ip"`
	Port     int    `yaml:"port" json:"port"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
	Database string `yaml:"database" json:"database"`
}

type userListImpl []*userImpl

func (ul userListImpl) ForEach(do func(user interfaces.User)) {
	for _, user := range ul {
		do(user)
	}
}

// Load the config at the given path
func (config *configImpl) Load() error {
	var err error

	data, err := ioutil.ReadFile(filepath.Join(config.Path, "config.yml"))
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(data, config)
	if err != nil {
		return err
	}

	if len(config.CSecretB64) > 0 {
		cs, err := base64.StdEncoding.DecodeString(config.CSecretB64)
		if err != nil {
			return err
		}
		config.CookieSecret = cs
	} else {
		cs := make([]byte, 32)
		_, err := rand.Read(cs)
		if err != nil {
			return err
		}
		config.CookieSecret = cs
		config.CSecretB64 = base64.StdEncoding.EncodeToString(cs)
	}

	for _, user := range config.Users {
		user.HostConf = config
	}

	return nil
}

func (config *configImpl) Connect() {
	for _, user := range config.Users {
		user.LoadGlobalScripts(config.Path)
		user.NewMessages = make(chan messages.Container, MessageBufferSize)
		user.InitNetworks()
	}
}

// Save the configuration file
func (config *configImpl) Save() error {
	for _, user := range config.Users {
		user.Save()
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(config.Path, "config.yml"), data, 0644)
	return err
}

func (config *configImpl) Autosave() error {
	if config.AutosaveConfig {
		return config.Save()
	}
	return nil
}

// GetUsers returns all users
func (config *configImpl) GetUsers() interfaces.UserList {
	return config.Users
}

func (config *configImpl) PurgeUnverifiedUsers() {
	if !config.Mail.Enabled {
		return
	}
	deleted := 0
	for i := range config.Users {
		j := i - deleted
		user := config.Users[j]
		if user.EmailVerify != nil && user.EmailVerify.HasExpired() {
			config.Users = append(config.Users[:j], config.Users[j+1:]...)
			deleted++
		}
	}
	config.Autosave()
}

// GetUser gets the user with the given email
func (config *configImpl) GetUser(email string) interfaces.User {
	email = strings.ToLower(email)
	deleted := 0
	for i := range config.Users {
		j := i - deleted
		user := config.Users[j]
		if config.Mail.Enabled && user.EmailVerify != nil && user.EmailVerify.HasExpired() {
			config.Users = append(config.Users[:j], config.Users[j+1:]...)
			deleted++
			if user.Email == email {
				config.Autosave()
				return nil
			}
			continue
		}
		if deleted > 0 {
			config.Autosave()
		}
		if user.Email == email {
			return user
		}
	}
	return nil
}

func (config *configImpl) CreateUser(email, password string) (user interfaces.User, token string, timed time.Time) {
	email = strings.ToLower(email)
	for _, u := range config.Users {
		if u.Email == email {
			return
		}
	}
	userInt := &userImpl{
		HostConf:    config,
		NewMessages: make(chan messages.Container, MessageBufferSize),
		Email:       email,
	}

	if config.Mail.Enabled {
		token = generateAuthToken()
		timed = time.Now().Add(1 * time.Hour)
		userInt.EmailVerify = &authToken{Token: token, Time: timed.Unix()}
	} else {
		userInt.EmailVerify = nil
	}

	userInt.SetPassword(password)
	config.Users = append(config.Users, userInt)
	config.Autosave()
	return user, token, timed
}

func (config *configImpl) GetSQLString() string {
	return fmt.Sprintf("%[1]s:%[2]s@tcp(%[3]s:%[4]d)/%[5]s",
		config.SQL.Username,
		config.SQL.Password,
		config.SQL.IP,
		config.SQL.Port,
		config.SQL.Database,
	)
}

func (config *configImpl) GetMail() interfaces.Mail {
	return config.Mail
}

func (config *configImpl) GetIDENTConfig() interfaces.IdentConf {
	return config.Ident
}

func (config *configImpl) GetPath() string {
	return config.Path
}

func (config *configImpl) TrustHeaders() bool {
	return config.TrustHeadersF
}

func (config *configImpl) GetAddr() string {
	return fmt.Sprintf("%[1]s:%[2]d", config.IP, config.Port)
}

func (config *configImpl) GetExternalAddr() string {
	return config.Address
}

func (config *configImpl) GetCookieSecret() []byte {
	return config.CookieSecret
}

func (config *configImpl) SecureCookies() bool {
	return config.HTTPSOnlyCookies
}
