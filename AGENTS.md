# AGENTS.md

This file provides guidance to Agents when working with code in this repository.

## Build Commands
- `make build`: Build with default settings
- `make dev`: Build with development versioning
- `make test`: Run all tests
- `make coverage`: Generate test coverage (coverage.out)
- `make coverage-html`: Generate HTML coverage report

## Test Commands
- Run single test: `go test -v ./internal/args -run TestExtractFlagValue`
- Run benchmark: `make bench`

## Testing Standards
- **File Structure**: Place test files in the same package as the code they test with `_test.go` suffix
- **Table-Driven Tests**: Organize test cases in tables with struct definitions for consistent testing
- **Assertions**: Use the `github.com/stretchr/testify/require` package for assertions with clear error messages
- **Subtests**: Use `t.Run()` for organizing tests into logical groups
- **Coverage**: Aim for high test coverage, especially for critical paths
- **Test Data**: Store test data files in `testdata` directories
- **Edge Cases**: Include tests for edge cases and invalid inputs
- **Isolation**: Design unit tests to test specific functionality in isolation
- **Package Names**: Use `package name_test` pattern for proper separation
- **Test Namespace**: Tests must be written in isolated `package name_test` packages. Do not place tests in the same package as production code to access unexported identifiers. If code cannot be tested from `package name_test`, treat that as a design problem in the production code and improve the code instead of bypassing the boundary.
- **Behavior Over Internals**: Tests must verify observable behavior and contracts, not private helpers, implementation details, or exact logging shapes.
- **No Log-Only Tests**: Do not add tests whose main purpose is to assert log messages, log levels, or logger field formatting unless logging itself is the exported feature under test.

## Code Style Guidelines
- **Formatting**: Use standard Go formatting (gofmt)
- **Imports**: Group imports (stdlib first, external, then internal)
- **Error Handling**: Check errors explicitly, return them early
- **Testing**: Use testify/require for assertions
- **Documentation**: Document public types and functions with comments starting with the element name
- **Logging**: Use zerolog with consistent log levels
- **Types**: Use generics where applicable
- **Naming**: Follow standard Go conventions:
  - Use PascalCase for exported elements
  - Use camelCase for non-exported elements
  - Respect idiomatic patterns (`value, ok` for lookups)
  - Use descriptive names but avoid unnecessary verbosity
  - Avoid abbreviations unless they're well-established
  - Keep receiver variable names consistent across methods

## Project Guidelines
- All comments and code must be in English (this is an open source project)
- Never add comments explaining how you modified code - write self-explanatory code
- Prioritize performance, readability, and maintainability
- Follow idiomatic Go patterns and don't modify them unnecessarily
- Respect existing conventions and patterns in the codebase
- Write code as if you have enterprise-level experience, focusing on robustness and clarity

## Agent Behavior
- Commands that are expected to work out of the box in this project, such as `go test`, `make test`, and `make build`, must always be run in their normal form first.
- If such a command fails, the agent must always report what happened and propose solution options before taking any workaround.
- The agent must never independently invent workarounds for such failures. This includes changing the command, altering the environment, creating temporary caches or directories, or introducing any other side effects without explicit user approval.
- The agent must first investigate the cause of the failure and prefer fixing the real problem instead of adding a workaround.
