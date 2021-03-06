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

// Package userlist contains the UserList type
package userlist

import (
	"strings"
)

// List is a wrapper for sorting user lists
type List []string

func (s List) Len() int {
	return len(s)
}
func (s List) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

// LevelOf gets the int level of the given rune
func LevelOf(r rune) int {
	switch r {
	case '~':
		return 5
	case '&':
		return 4
	case '@':
		return 3
	case '%':
		return 2
	case '+':
		return 1
	default:
		return 0
	}
}

// NameOf gets a basic name of the given level
func NameOf(level int) string {
	switch level {
	case 5:
		return "owner"
	case 4:
		return "admin"
	case 3:
		return "operator"
	case 2:
		return "half-op"
	case 1:
		return "voice"
	default:
		return ""
	}
}

// LevelOfByte gets the int level of the given byte
func LevelOfByte(b byte) int {
	return LevelOf(rune(b))
}

// PrefixOf gets the rune prefix of the given level
func PrefixOf(level int) rune {
	switch level {
	case 5:
		return '~'
	case 4:
		return '&'
	case 3:
		return '@'
	case 2:
		return '%'
	case 1:
		return '+'
	default:
		return 0
	}
}

func (s List) Less(i, j int) bool {
	levelI := LevelOfByte(s[i][0])
	levelJ := LevelOfByte(s[j][0])
	if levelI > levelJ {
		return true
	} else if levelI < levelJ {
		return false
	} else {
		return strings.ToLower(s[i]) < strings.ToLower(s[j])
	}
}

// Merge the given string list with this user list
func (s List) Merge(other []string) List {
Outer:
	for _, str := range other {
		if len(str) == 0 {
			continue
		}
		for _, strOld := range s {
			if str == strOld {
				continue Outer
			}
		}
		s = append(s, str)
	}
	return s
}

// Contains checks if the given user is in this UserList
func (s List) Contains(user string) (bool, int) {
	for i, u := range s {
		if user == u {
			return true, i
		} else if LevelOfByte(u[0]) > 0 && user == u[1:] {
			return true, i
		}
	}
	return false, -1
}

// SetPrefix sets the prefix of the given user
func (s List) SetPrefix(user string, prefix string) bool {
	if LevelOfByte(prefix[0]) == 0 {
		prefix = ""
	}
	if LevelOfByte(user[0]) > 0 {
		user = user[1:]
	}
	for i, u := range s {
		if LevelOfByte(u[0]) > 0 {
			if u[1:] == user {
				s[i] = prefix + user
				return true
			}
		} else {
			if u == user {
				s[i] = prefix + user
				return true
			}
		}
	}
	return false
}
