// Copyright 2020 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package cmd_test

import (
	"bytes"

	corecmd "github.com/juju/cmd"
	"github.com/juju/cmd/cmdtesting"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju-restore/cmd"
)

type InteractionsSuite struct {
	testing.IsolationSuite

	ctx   *corecmd.Context
	stdin bytes.Buffer
	ui    *cmd.UserInteractions
}

var _ = gc.Suite(&InteractionsSuite{})

func (s *InteractionsSuite) SetUpTest(c *gc.C) {
	s.IsolationSuite.SetUpTest(c)
	s.ctx = cmdtesting.Context(c)
	s.ctx.Stdin = &s.stdin
	s.ui = cmd.NewUserInteractions(s.ctx)
}

func (s *InteractionsSuite) TestUserConfirmEnter(c *gc.C) {
	s.stdin.WriteString("\n")
	c.Assert(s.ui.UserConfirmYes(), jc.Satisfies, cmd.IsUserAbortedError)
	c.Assert(cmdtesting.Stderr(s.ctx), gc.Equals, "")
	c.Assert(cmdtesting.Stdout(s.ctx), gc.Equals, "")
}

func (s *InteractionsSuite) TestUserConfirmExplicitNo(c *gc.C) {
	s.stdin.WriteString("n")
	c.Assert(s.ui.UserConfirmYes(), jc.Satisfies, cmd.IsUserAbortedError)
	c.Assert(cmdtesting.Stderr(s.ctx), gc.Equals, "")
	c.Assert(cmdtesting.Stdout(s.ctx), gc.Equals, "")

	s.stdin.WriteString("N")
	c.Assert(s.ui.UserConfirmYes(), jc.Satisfies, cmd.IsUserAbortedError)
	c.Assert(cmdtesting.Stderr(s.ctx), gc.Equals, "")
	c.Assert(cmdtesting.Stdout(s.ctx), gc.Equals, "")
}

func (s *InteractionsSuite) TestUserConfirmExplicitYes(c *gc.C) {
	s.stdin.WriteString("y")
	c.Assert(s.ui.UserConfirmYes(), jc.ErrorIsNil)
	c.Assert(cmdtesting.Stderr(s.ctx), gc.Equals, "")
	c.Assert(cmdtesting.Stdout(s.ctx), gc.Equals, "")

	s.stdin.WriteString("Y")
	c.Assert(s.ui.UserConfirmYes(), jc.ErrorIsNil)
	c.Assert(cmdtesting.Stderr(s.ctx), gc.Equals, "")
	c.Assert(cmdtesting.Stdout(s.ctx), gc.Equals, "")
}

func (s *InteractionsSuite) TestNotify(c *gc.C) {
	s.ui.Notify("must be fun to be on stdout")
	c.Assert(cmdtesting.Stderr(s.ctx), gc.Equals, "")
	c.Assert(cmdtesting.Stdout(s.ctx), gc.Equals, "must be fun to be on stdout")
}
