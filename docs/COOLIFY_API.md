# Coolify API reference (extracted)

Reference for the orchestration backend Viro builds on. Extracted from the upstream
`coollabsio/coolify` **v4.1.2** routes (`routes/api.php`). Authoritative source: the
Coolify instance's own OpenAPI at `/docs/api` and <https://coolify.io/docs>.

## Base & auth
- **Base path:** `https://<coolify-host>/api/v1`
- **Auth:** `Authorization: Bearer <token>` (Laravel Sanctum personal access token)
- **Abilities:** tokens carry `read`, `write`, and/or `deploy`; sensitive fields are masked
  unless the token is marked to see them.
- **Health (no auth):** `GET /api/health`, `GET /api/v1/health`

## Endpoints used by Viro

### Meta
| Method | Path | Purpose |
|---|---|---|
| GET | `/version` | Coolify instance version |
| GET | `/teams`, `/teams/current`, `/teams/{id}` | Teams (Coolify tenancy) |
| GET | `/resources` | All resources for the team |

### Projects & environments
| Method | Path |
|---|---|
| GET/POST | `/projects`, `/projects/{uuid}` |
| GET/POST/DELETE | `/projects/{uuid}/environments[/{name}]` |

### Applications
| Method | Path | Purpose |
|---|---|---|
| GET | `/applications` | list |
| POST | `/applications/public` | create from public git repo |
| POST | `/applications/private-github-app` | create from a GitHub App repo |
| POST | `/applications/private-deploy-key` | create from a deploy-key repo |
| POST | `/applications/dockerfile` | create from inline Dockerfile |
| POST | `/applications/dockerimage` | create from a Docker image |
| GET/PATCH/DELETE | `/applications/{uuid}` | get / update / delete |
| GET/POST/PATCH/DELETE | `/applications/{uuid}/envs[...]` | env vars (incl. `/envs/bulk`) |
| GET | `/applications/{uuid}/logs` | logs |
| GET/POST/PATCH/DELETE | `/applications/{uuid}/storages[...]` | persistent storage |
| GET/POST | `/applications/{uuid}/start` | **deploy** |
| GET/POST | `/applications/{uuid}/stop` | stop |
| GET/POST | `/applications/{uuid}/restart` | restart |

### Deployments
| Method | Path |
|---|---|
| GET/POST | `/deploy` (by `?uuid=` or `?tag=`) |
| GET | `/deployments`, `/deployments/{uuid}` |
| GET | `/deployments/applications/{uuid}` |
| POST | `/deployments/{uuid}/cancel` |

### Databases (standalone)
Create: `POST /databases/{postgresql|mysql|mariadb|mongodb|redis|clickhouse|dragonfly|keydb}`
Manage: `GET /databases`, `GET/PATCH/DELETE /databases/{uuid}`,
`GET/POST /databases/{uuid}/{start|stop|restart}`, plus `/envs`, `/storages`, `/backups`.

### Services
`GET/POST /services`, `GET/PATCH/DELETE /services/{uuid}`, `/services/{uuid}/{start|stop|restart}`,
plus `/envs`, `/storages`, `/scheduled-tasks`.

### Servers
`GET /servers`, `GET /servers/{uuid}` (+ `/domains`, `/resources`, `/validate`),
`POST /servers`, `PATCH/DELETE /servers/{uuid}`.

### Security (SSH keys)
`GET/POST /security/keys`, `GET/PATCH/DELETE /security/keys/{uuid}`.

## Notes for Viro's mapping
- A Viro **App** maps to a Coolify **application**; deploy = `POST /applications/{uuid}/start`.
- A Viro **Database** maps to a Coolify **standalone database**.
- Viro **organizations/teams/users/billing** live in the Viro control-plane DB — Coolify only
  knows its own single team per token. Viro stores the Coolify resource UUIDs alongside its
  own records.
- Custom domains + TLS: set the application `fqdn` (via update) — Coolify provisions Let's
  Encrypt through its Traefik proxy.
