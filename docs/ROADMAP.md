# Reign Roadmap

This document outlines planned features, improvements, and future considerations for Reign.

---

## Current Version: 0.1.0

Initial release with core functionality:

- [x] Docker Compose service management
- [x] Native binary service management
- [x] SQLite persistent storage
- [x] REST API for service control
- [x] Infrastructure service priority startup
- [x] Automatic image pulling before start
- [x] Health check reporting
- [x] Event logging/audit trail
- [x] Graceful shutdown handling

---

## Version 0.2.0 - Enhanced Monitoring

**Analytics and Observability:**

- [ ] Container resource metrics (CPU, memory, network)
- [ ] Historical metrics storage with configurable retention
- [ ] Prometheus metrics endpoint (`/metrics`)
- [ ] Service dependency graphs
- [ ] Log aggregation endpoint for recent container logs

**Health Checks:**

- [ ] Active health check probes (HTTP, TCP, exec)
- [ ] Configurable health check intervals per service
- [ ] Health check failure thresholds before marking unhealthy
- [ ] Webhook notifications on health state changes

---

## Version 0.3.0 - Security and Multi-tenancy

**Authentication:**

- [ ] API key authentication
- [ ] Multiple API keys with different permissions
- [ ] Rate limiting per API key
- [ ] Audit logging of API access

**Access Control:**

- [ ] Read-only vs read-write API keys
- [ ] Service-scoped API keys (can only manage specific services)
- [ ] IP allowlist for API access

---

## Version 0.4.0 - Service Dependencies

**Dependency Management:**

- [ ] Define service dependencies (service A depends on service B)
- [ ] Automatic dependency resolution on start
- [ ] Wait for dependent services to be healthy before starting
- [ ] Prevent stopping a service if dependents are running
- [ ] Circular dependency detection

**Configuration:**

```json
{
  "id": "myapp",
  "depends_on": ["postgres", "redis"],
  "wait_for_healthy": true
}
```

---

## Version 0.5.0 - Scheduled Operations

**Scheduling:**

- [ ] Scheduled service restarts (daily, weekly, cron expression)
- [ ] Scheduled image pulls without restart
- [ ] Maintenance windows (prevent restarts during certain hours)
- [ ] Scheduled enable/disable for services

**Backup Integration:**

- [ ] Pre-backup hooks (stop service, run command)
- [ ] Post-backup hooks (start service)
- [ ] Integration with common backup tools

---

## Version 0.6.0 - Web Dashboard

**Dashboard Features:**

- [ ] Real-time service status overview
- [ ] Start/stop/restart controls
- [ ] Log viewer
- [ ] Metrics graphs
- [ ] Event history timeline
- [ ] Service configuration editor

**Implementation:**

- Server-rendered HTML with Go templates
- Embedded static assets (CSS, JS)
- Plain JavaScript for interactivity
- No external frontend dependencies

---

## Future Considerations

### Configuration Management

Potential features for managing service configurations:

- [ ] Template support for docker-compose.yml
- [ ] Environment variable injection
- [ ] Secrets management integration (Vault, etc.)
- [ ] Configuration drift detection

### Backup and Restore

- [ ] Export/import service definitions
- [ ] Database backup scheduling
- [ ] Migration tools between Reign instances

---

## Technical Debt

Items to address as the project matures:

- [ ] Increase test coverage to >80%
- [ ] Add integration tests with actual Docker Compose
- [ ] Performance testing with many services
- [ ] Documentation improvements
- [ ] Example systemd unit files and installation guide

---

## Contributing Ideas

If extending Reign, consider these areas:

1. **Notification integrations** - Slack, Discord, email notifications
2. **CLI tool** - Command-line interface for reign management
3. **Import tools** - Import existing systemd service definitions
4. **Ansible/Terraform providers** - Infrastructure-as-code support
