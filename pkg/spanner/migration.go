// Copyright (c) 2020 Mercari, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of
// this software and associated documentation files (the "Software"), to deal in
// the Software without restriction, including without limitation the rights to
// use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
// the Software, and to permit persons to whom the Software is furnished to do so,
// subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
// FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
// COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
// IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
// CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

package spanner

import (
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strconv"

	"cloud.google.com/go/spanner/spansql"
)

var (
	// migrationFileRegex matches the following patterns
	// 001.sql
	// 001_name.sql
	// 001_name.up.sql
	migrationFileRegex = regexp.MustCompile(`^([0-9]+)(?:_([a-zA-Z0-9_\-]+))?(\.up)?\.sql$`)

	MigrationNameRegex = regexp.MustCompile(`[a-zA-Z0-9_\-]+`)

	dmlRegex = regexp.MustCompile("^(UPDATE|DELETE)[\t\n\f\r ].*")
)

const (
	statementKindDDL statementKind = "DDL"
	statementKindDML statementKind = "DML"
)

type (
	// migration represents the parsed migration file. e.g. version_name.sql
	Migration struct {
		// Version is the version of the migration
		Version uint

		// Name is the name of the migration
		Name string

		// Statements is the migration statements
		Statements []string

		kind statementKind
	}

	Migrations []*Migration

	statementKind string
)

func (ms Migrations) Len() int {
	return len(ms)
}

func (ms Migrations) Swap(i, j int) {
	ms[i], ms[j] = ms[j], ms[i]
}

func (ms Migrations) Less(i, j int) bool {
	return ms[i].Version < ms[j].Version
}

func LoadMigrations(dir string) (Migrations, error) {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var migrations Migrations

	versions := map[uint64]string{}

	for _, f := range files {
		if f.IsDir() {
			continue
		}

		matches := migrationFileRegex.FindStringSubmatch(f.Name())
		if len(matches) != 4 {
			continue
		}

		version, err := strconv.ParseUint(matches[1], 10, 64)
		if err != nil {
			continue
		}

		fileName := f.Name()

		file, err := ioutil.ReadFile(filepath.Join(dir, fileName))
		if err != nil {
			continue
		}

		statements, err := ddlToStatements(f.Name(), file)
		if err != nil {
			nstatements, nerr := dmlToStatements(f.Name(), file)
			if nerr != nil {
				return nil, errors.New("failed to parse DDL/DML statements")
			}
			statements = nstatements
		}

		kind, err := inspectStatementsKind(statements)
		if err != nil {
			return nil, err
		}

		migrations = append(migrations, &Migration{
			Version:    uint(version),
			Name:       matches[2],
			Statements: statements,
			kind:       kind,
		})

		if prevFileName, ok := versions[version]; ok {
			return nil, fmt.Errorf("colliding version number \"%d\" between file names \"%s\" and \"%s\"", version, prevFileName, fileName)
		}
		versions[version] = fileName
	}

	return migrations, nil
}

func ddlToStatements(filename string, data []byte) ([]string, error) {
	ddl, err := spansql.ParseDDL(filename, string(data))
	if err != nil {
		return nil, err
	}

	var statements []string
	for _, stmt := range ddl.List {
		statements = append(statements, stmt.SQL())
	}

	return statements, nil
}

func dmlToStatements(filename string, data []byte) ([]string, error) {
	dml, err := spansql.ParseDML(filename, string(data))
	if err != nil {
		return nil, err
	}

	var statements []string
	for _, stmt := range dml.List {
		statements = append(statements, stmt.SQL())
	}

	return statements, nil
}

func inspectStatementsKind(statements []string) (statementKind, error) {
	kindMap := map[statementKind]uint64{
		statementKindDDL: 0,
		statementKindDML: 0,
	}

	for _, s := range statements {
		if isDML(s) {
			kindMap[statementKindDML]++
		} else {
			kindMap[statementKindDDL]++
		}
	}

	if kindMap[statementKindDML] > 0 {
		if kindMap[statementKindDDL] > 0 {
			return "", errors.New("cannot specify DDL and DML at same migration file")
		}

		return statementKindDML, nil
	}

	return statementKindDDL, nil
}

func isDML(statement string) bool {
	return dmlRegex.Match([]byte(statement))
}
