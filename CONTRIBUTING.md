# Contributing to httploggen

Thank you for your interest in contributing! We welcome contributions from the community.

## How to Contribute

### Reporting Issues

- Use the GitHub issue tracker to report bugs
- Describe the issue clearly with steps to reproduce
- Include your Go version, OS, and any relevant logs

### Submitting Changes

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes
4. Test your changes thoroughly
5. Commit with clear messages (`git commit -m 'Add amazing feature'`)
6. Push to your branch (`git push origin feature/amazing-feature`)
7. Open a Pull Request

## Development Setup

```bash
# Clone your fork
git clone https://github.com/YOUR_USERNAME/httploggen.git
cd httploggen

# Build
go build -o httploggen main.go

# Test
go run main.go --endpoint http://localhost:8080 --period 1s --total-time 10s
```

## Code Style

- Follow standard Go conventions
- Run `go fmt` before committing
- Add comments for exported functions and types
- Keep functions focused and testable

## Testing

Before submitting a PR:

1. Ensure the code compiles: `go build`
2. Test basic functionality: `go run main.go --help`
3. Test actual log generation with a local endpoint
4. Verify stats output is correct

## Adding New Features

When adding new features:

1. Update the README.md
2. Add example usage if applicable
3. Update command-line help text
4. Consider backward compatibility

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
