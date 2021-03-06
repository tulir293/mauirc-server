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
	"fmt"
	"strconv"
	"strings"
	"time"

	msg "github.com/sorcix/irc"
	irc "maunium.net/go/libmauirc"
	"maunium.net/go/mauirc-server/common/messages"
	"maunium.net/go/mauirc-server/database"
	"maunium.net/go/mauirc-server/ident"
	"maunium.net/go/mauirc-server/interfaces"
	"maunium.net/go/mauirc-server/util/preview"
	"maunium.net/go/mauirc-server/util/split"
	"maunium.net/go/mauirc-server/util/userlist"
	"maunium.net/go/maulogger"
)

type netImpl struct {
	Name     string   `yaml:"name" json:"name"`
	Nick     string   `yaml:"nick" json:"nick"`
	User     string   `yaml:"user" json:"user"`
	Realname string   `yaml:"realname" json:"realname"`
	Password string   `yaml:"password" json:"password"`
	IP       string   `yaml:"ip" json:"ip"`
	Port     uint16   `yaml:"port" json:"port"`
	SSL      bool     `yaml:"ssl" json:"ssl"`
	Chs      []string `yaml:"channels" json:"channels"`

	Owner       *userImpl                      `yaml:"-" json:"-"`
	IRC         irc.Connection                 `yaml:"-" json:"-"`
	Scripts     []interfaces.Script            `yaml:"-" json:"-"`
	ChannelInfo cdlImpl                        `yaml:"-" json:"-"`
	ChannelList []string                       `yaml:"-" json:"-"`
	WhoisData   map[string]*messages.WhoisData `yaml:"-" json:"-"`
	IdentPort   int                            `yaml:"-" json:"-"`
	Sublogger   *maulogger.Sublogger           `yaml:"-" json:"-"`
}

func (net *netImpl) Save() {
	net.Chs = []string{}
	for ch := range net.ChannelInfo {
		net.Chs = append(net.Chs, ch)
	}
}

// Open an IRC connection
func (net *netImpl) Open() {
	i := irc.Create(net.Nick, net.User, irc.IPv4Address{IP: net.IP, Port: net.Port})
	i.SetRealName(net.Realname)
	i.SetQuitMessage("mauIRC server shutting down...")
	i.SetUseTLS(net.SSL)

	net.Sublogger = maulogger.CreateSublogger(net.Owner.GetNameFromEmail()+"/"+net.Name, maulogger.LevelDebug)
	go func() {
		for err := range i.Errors() {
			net.Sublogger.Error(err.Error())
		}
	}()
	if maulogger.DefaultLogger.PrintLevel == 0 {
		i.SetDebugWriter(net.Sublogger)
	}

	if len(net.Password) > 0 {
		i.AddAuth(&irc.PasswordAuth{Password: net.Password})
	}

	for _, ch := range net.Chs {
		net.ChannelInfo.Put(&chanDataImpl{Network: net.Name, Name: ch})
	}
	net.WhoisData = make(map[string]*messages.WhoisData)

	net.IRC = i

	i.AddHandler(msg.PRIVMSG, net.privmsg)
	i.AddHandler(msg.NOTICE, net.privmsg)
	i.AddHandler(msg.INVITE, net.invite)
	i.AddHandler(msg.RPL_INVITING, net.invited)
	i.AddHandler(msg.ERR_USERONCHANNEL, net.inviteFail)
	i.AddHandler("CPRIVMSG", net.privmsg)
	i.AddHandler("CNOTICE", net.privmsg)
	i.AddHandler("CTCP_ACTION", net.action)
	i.AddHandler(msg.JOIN, net.join)
	i.AddHandler(msg.PART, net.part)
	i.AddHandler(msg.KICK, net.kick)
	i.AddHandler(msg.MODE, net.mode)
	i.AddHandler(msg.TOPIC, net.topic)
	i.AddHandler(msg.NICK, net.nick)
	i.AddHandler(msg.QUIT, net.quit)
	i.AddHandler("DISCONNECTED", net.disconnected)
	i.AddHandler(msg.RPL_WELCOME, net.connected)
	i.AddHandler(msg.RPL_NAMREPLY, net.userlist)
	i.AddHandler(msg.RPL_ENDOFNAMES, net.userlistend)
	i.AddHandler(msg.RPL_LIST, net.chanlist)
	i.AddHandler(msg.RPL_LISTEND, net.chanlistend)
	i.AddHandler(msg.RPL_TOPIC, net.topicresp)
	i.AddHandler(msg.RPL_TOPICWHOTIME, net.topicset)
	i.AddHandler(msg.ERR_CHANOPRIVSNEEDED, net.noperms)
	i.AddHandler(msg.RPL_AWAY, net.isAway)
	i.AddHandler(msg.RPL_WHOISUSER, net.whoisUser)
	i.AddHandler(msg.RPL_WHOISSERVER, net.whoisServer)
	i.AddHandler(msg.RPL_WHOISOPERATOR, net.whoisOperator)
	i.AddHandler(msg.RPL_WHOISIDLE, net.whoisIdle)
	i.AddHandler(msg.RPL_ENDOFWHOIS, net.whoisEnd)
	i.AddHandler(msg.RPL_WHOISCHANNELS, net.whoisChannels)
	i.AddHandler("617", net.whoisSecure)
	i.AddHandler("*", net.rawHandler)

	if err := net.Connect(); err != nil {
		log.Errorf("Failed to connect to %s:%d: %s\n", net.IP, net.Port, err)
	}
}

func (net *netImpl) AddIdent() error {
	addr := strings.Split(net.IRC.LocalAddr().String(), ":")
	if len(addr) < 0 {
		return fmt.Errorf("Invalid local address (%s)", net.IRC.LocalAddr().String())
	}

	port, err := strconv.Atoi(addr[1])
	if err != nil {
		return fmt.Errorf("Invalid port (%s): %s", addr[1], err)
	}

	ident.Ports[port] = net.Owner.GetNameFromEmail()
	if err != nil {
		return fmt.Errorf("Failed to add ident: %s", err)
	}
	net.IdentPort = port
	log.Debugf("Added ident %d -> %s (%s)\n", port, net.Owner.GetNameFromEmail(), net.IRC.LocalAddr().String())

	return nil
}

func (net *netImpl) RemoveIdent() bool {
	if net.IdentPort == 0 {
		return false
	}

	delete(ident.Ports, net.IdentPort)
	log.Debugf("Deleted ident %d -> %s\n", net.IdentPort, net.Owner.GetNameFromEmail())
	net.IdentPort = 0
	return true
}

func (net *netImpl) Connect() error {
	err := net.IRC.Connect()
	if err != nil {
		return err
	}
	return net.AddIdent()
}

// Close the IRC connection.
func (net *netImpl) Disconnect() {
	if net.IRC.Connected() {
		net.IRC.Quit()
		net.RemoveIdent()
	}
}

func (net *netImpl) ForceDisconnect() {
	net.IRC.Disconnect()
	net.RemoveIdent()
}

func (net *netImpl) IsConnected() bool {
	return net.IRC.Connected()
}

// ReceiveMessage stores the message and sends it to the client
func (net *netImpl) ReceiveMessage(channel, sender, command, message string) {
	msg := messages.Message{Network: net.Name, Channel: channel, Timestamp: time.Now().Unix(), Sender: sender, Command: command, Message: message}

	if msg.Sender == net.IRC.GetNick() || (command == "nick" && message == net.IRC.GetNick()) {
		msg.OwnMsg = true
	} else {
		msg.OwnMsg = false
	}

	if msg.Channel == "AUTH" || msg.Channel == "*" {
		return
	} else if msg.Channel == net.IRC.GetNick() {
		if len(msg.Sender) > 0 && net.GetActiveChannels().Has(msg.Sender) {
			net.GetActiveChannels().Put(&chanDataImpl{Network: net.Name, Name: msg.Sender})
		}
		msg.Channel = msg.Sender
	}

	var evt = &interfaces.Event{Message: msg, Network: net, Cancelled: false}
	net.RunScripts(evt, true)
	if evt.Cancelled {
		return
	}
	msg = evt.Message

	net.InsertAndSend(msg)
}

// SendMessage sends the given message to the given channel
func (net *netImpl) SendMessage(channel, command, message string) {
	msg := messages.Message{Network: net.Name, Channel: channel, Timestamp: time.Now().Unix(), Sender: net.IRC.GetNick(), Command: command, Message: message, OwnMsg: true}

	var evt = &interfaces.Event{Message: msg, Network: net, Cancelled: false}
	net.RunScripts(evt, true)
	if evt.Cancelled {
		return
	}
	msg = evt.Message

	if splitted := split.All(msg.Message); len(splitted) > 1 {
		for _, piece := range splitted {
			net.SendMessage(msg.Channel, msg.Command, piece)
		}
		return
	}

	if net.sendToIRC(msg) {
		net.InsertAndSend(msg)
	}
}

func (net *netImpl) sendToIRC(msg messages.Message) bool {
	if !strings.HasPrefix(msg.Channel, "*") {
		switch msg.Command {
		case "privmsg":
			net.IRC.Privmsg(msg.Channel, msg.Message)
			return true
		case "action":
			net.IRC.Action(msg.Channel, msg.Message)
			return true
		case "topic":
			net.IRC.Topic(msg.Channel, msg.Message)
		case "join":
			net.IRC.Join(msg.Channel, "")
		case "part":
			net.IRC.Part(msg.Channel, msg.Message)
		case "nick":
			net.IRC.SetNick(msg.Message)
		case "whois":
			net.IRC.Whois(msg.Channel)
		case "invite":
			net.IRC.Invite(msg.Message, msg.Channel)
		}
	}
	return false
}

// SwitchNetwork sends the given message to another network
func (net *netImpl) SwitchMessageNetwork(msg messages.Message, receiving bool) bool {
	newNet := net.Owner.GetNetwork(msg.Network)
	if newNet != nil {
		if receiving {
			newNet.ReceiveMessage(msg.Channel, msg.Sender, msg.Command, msg.Message)
		} else {
			newNet.SendMessage(msg.Channel, msg.Command, msg.Message)
		}
		return true
	}
	return false
}

// InsertAndSend inserts the given message into the database and sends it to the client
func (net *netImpl) InsertAndSend(msg messages.Message) {
	if len(msg.Command) == 0 {
		return
	}
	msg.Preview, _ = preview.GetPreview(msg.Message)
	msg.ID = database.Insert(net.Owner.Email, msg)
	net.Owner.SendMessage(messages.Container{Type: messages.MsgMessage, Object: msg})
}

func (net *netImpl) GetOwner() interfaces.User {
	return net.Owner
}

func (net *netImpl) GetName() string {
	return net.Name
}

func (net *netImpl) GetNick() string {
	return net.IRC.GetNick()
}

func (net *netImpl) SetName(name string) {
	net.Name = name
	net.Sublogger.SetModule(net.Owner.GetNameFromEmail() + "/" + name)
	net.Owner.HostConf.Autosave()
}

func (net *netImpl) SetNick(nick string) {
	if net.IsConnected() {
		net.SendMessage("", "nick", nick)
	}
	net.Nick = nick
}

func (net *netImpl) SetRealname(realname string) {
	net.IRC.SetRealName(realname)
	net.Realname = realname
}

func (net *netImpl) SetUser(user string) {
	net.User = user
	net.Owner.HostConf.Autosave()
}

func (net *netImpl) SetIP(ip string) {
	net.IP = ip
	net.Owner.HostConf.Autosave()
}

func (net *netImpl) SetPort(port uint16) {
	net.Port = port
	net.Owner.HostConf.Autosave()
}

func (net *netImpl) SetSSL(ssl bool) {
	net.SSL = ssl
	if net.IRC != nil {
		net.IRC.SetUseTLS(ssl)
	}
	net.Owner.HostConf.Autosave()
}

func (net *netImpl) GetNetData() messages.NetData {
	return messages.NetData{
		Name:      net.Name,
		IP:        net.IP,
		Port:      net.Port,
		SSL:       net.SSL,
		User:      net.User,
		Realname:  net.Realname,
		Nick:      net.Nick,
		Connected: net.IsConnected(),
	}
}

func (net *netImpl) GetActiveChannels() interfaces.ChannelDataList {
	return net.ChannelInfo
}

func (net *netImpl) GetAllChannels() []string {
	return net.ChannelList
}

func (net *netImpl) Tunnel() irc.Tunnel {
	return net.IRC
}

func (net *netImpl) GetScripts() []interfaces.Script {
	return net.Scripts
}

func (net *netImpl) AddScript(s interfaces.Script) bool {
	for i := 0; i < len(net.Scripts); i++ {
		if net.Scripts[i].GetName() == s.GetName() {
			net.Scripts[i] = s
			return false
		}
	}
	net.Scripts = append(net.Scripts, s)
	net.SaveScripts(net.Owner.HostConf.Path)
	return true
}

func (net *netImpl) RemoveScript(name string) bool {
	for i := 0; i < len(net.Scripts); i++ {
		if net.Scripts[i].GetName() == name {
			net.Scripts = DeleteScript(net.Scripts, i)
			return true
		}
	}
	net.SaveScripts(net.Owner.HostConf.Path)
	return false
}

func (net *netImpl) GetWhoisData(name string) *messages.WhoisData {
	data, ok := net.WhoisData[name]
	if !ok {
		net.WhoisData[name] = &messages.WhoisData{Nick: name, Channels: make(map[string]string)}
		return net.WhoisData[name]
	}
	return data
}

func (net *netImpl) GetWhoisDataIfExists(name string) *messages.WhoisData {
	data, ok := net.WhoisData[name]
	if !ok {
		return nil
	}
	return data
}

func (net *netImpl) RemoveWhoisData(name string) {
	net.WhoisData[name] = nil
	delete(net.WhoisData, name)
}

type chanDataImpl struct {
	Network           string              `yaml:"network" json:"network"`
	Name              string              `yaml:"name" json:"name"`
	UserList          userlist.List       `yaml:"userlist" json:"userlist"`
	Topic             string              `yaml:"topic" json:"topic"`
	TopicSetBy        string              `yaml:"topicsetby" json:"topicsetby"`
	TopicSetAt        int64               `yaml:"topicsetat" json:"topicsetat"`
	ModeList          interfaces.ModeList `yaml:"modes" json:"modes"`
	ReceivingUserList bool                `yaml:"-" json:"-"`
}

func (cd *chanDataImpl) GetUsers() []string {
	return cd.UserList
}

func (cd *chanDataImpl) GetName() string {
	return cd.Name
}

func (cd *chanDataImpl) GetTopic() string {
	return cd.Topic
}

func (cd *chanDataImpl) GetNetwork() string {
	return cd.Network
}

func (cd *chanDataImpl) Modes() interfaces.ModeList {
	return cd.ModeList
}

type cdlImpl map[string]*chanDataImpl

func (cdl cdlImpl) Get(channel string) (interfaces.ChannelData, bool) {
	val, ok := cdl[strings.ToLower(channel)]
	return val, ok
}

func (cdl cdlImpl) get(channel string) *chanDataImpl {
	return cdl[strings.ToLower(channel)]
}

func (cdl cdlImpl) Put(data interfaces.ChannelData) {
	dat, ok := data.(*chanDataImpl)
	if ok {
		cdl[strings.ToLower(data.GetName())] = dat
	}
}

func (cdl cdlImpl) Remove(channel string) {
	delete(cdl, strings.ToLower(channel))
}

func (cdl cdlImpl) Has(channel string) bool {
	_, ok := cdl[strings.ToLower(channel)]
	return ok
}

func (cdl cdlImpl) ForEach(do func(interfaces.ChannelData)) {
	for _, val := range cdl {
		do(val)
	}
}
