# Artifax — Plan v1

Servicio self-hosted en Go para publicar documentos HTML subidos con `curl` por agentes. Un solo binario, SQLite + disco, sin servicios externos. Pensado para **un único admin**. Inspirado en [postplan](https://github.com/immanuel-peter/postplan).

## Principios

- Un binario, un volumen `/data`. Backup = copiar `/data`.
- Single user: existe el admin y existe el público. Un usuario autenticado sin permisos de admin es tratado exactamente igual que un anónimo.
- Los documentos se identifican por un **id nanoid**, nunca por nombre de archivo. El id es el enlace y es estable al sobrescribir.
- La app decide el acceso (no reglas en el proxy). Todo se sirve tras HTTPS.

## Stack

- Go 1.23+, `net/http` estándar (routing con método + patrón).
- `modernc.org/sqlite` (Go puro, sin CGO), modo WAL, migraciones SQL embebidas.
- `coreos/go-oidc/v3` + `golang.org/x/oauth2` (modo SSO).
- `golang.org/x/net/html` para validar HTML.
- `html/template` + `embed` para índice y panel. Sin build de frontend.
- Ids nanoid generados con `crypto/rand` en `internal/ids`, sin dependencia externa.
- Imagen Docker distroless, volumen `/data`.

## Identificadores

- Docs: nanoid de 12 chars, alfabeto `0-9a-zA-Z`.
- Tokens: `ax_` + 32 bytes aleatorios en base64url. Se guarda solo el hash SHA-256 y un prefijo visible.

## Visibilidad

| Estado     | Se ve en `/d/{id}`   | Aparece en `/` |
|------------|----------------------|----------------|
| `public`   | Todos                | Todos          |
| `unlisted` | Todos con el enlace  | Solo admin     |
| `private`  | Solo admin           | Solo admin     |

Por defecto al subir: `private`.

## Autenticación

### Humanos — `AUTH_MODE`

- **`sso`**: cliente OIDC nativo contra Authelia (u otro IdP OIDC). Es admin quien pertenezca a alguno de `ADMIN_GROUPS` (claim `groups`). Si un usuario sin grupo admin hace login, no se crea sesión y se le muestra "sin acceso".
- **`basic`**: formulario de login con `ADMIN_EMAIL` + `ADMIN_PASSWORD` definidos en env. Comparación en tiempo constante y rate limit básico por IP.
- Sin auth configurada: la app arranca, sirve solo lo público y el panel queda deshabilitado.

Ambos modos crean una sesión en servidor (tabla `sessions`). La cookie es `HttpOnly`, `Secure` y `SameSite=Lax`. Las peticiones POST del navegador pasan por `http.CrossOriginProtection` de Go 1.25 (`Sec-Fetch-Site`/`Origin`) en lugar de tokens CSRF. Los mensajes flash del panel viajan en una cookie de un solo uso.

### Agentes

- Bearer tokens creados y revocados desde `/admin`. El secreto se muestra una sola vez.
- Todos los tokens tienen permisos completos.
- Opcional: `ARTIFAX_BOOTSTRAP_TOKEN` se siembra si no hay ningún token creado.

## Rutas

| Ruta | Descripción |
|---|---|
| `GET /` | Índice: tarjetas con título, descripción, fecha y búsqueda. Anónimo ve los `public`, admin ve todo con una etiqueta de visibilidad. Botón de Login o, si eres admin, botones de Panel y Logout. |
| `GET /d/{id}` | Sirve el documento según su visibilidad. Si es privado y no hay sesión de admin, da 404 (no se revela que existe), salvo cuando no hay sesión, que redirige al login y vuelve al doc. |
| `/admin` | Panel (solo admin). |
| `/auth/login`, `/auth/callback`, `/auth/logout` | Flujo de login (OIDC o formulario basic). |
| `/api/*` | API para agentes. |
| `GET /openapi.json`, `GET /healthz` | Contrato de la API y estado. |

## API (`Authorization: Bearer ax_…`)

| Método | Ruta | Descripción |
|---|---|---|
| POST | `/api/docs` | Crea un doc y devuelve `201 {id, url, ...}`. Acepta multipart (`file`, `title`, `description`, `visibility`) o cuerpo `text/html` en bruto con esos campos como query params. |
| PUT | `/api/docs/{id}` | Reemplaza el contenido; los metadatos son opcionales. Si el id no existe, 404. |
| GET | `/api/docs` | Lista. Filtros `?q=` y `?visibility=`; paginación `?cursor=` / `?limit=`. |
| GET | `/api/docs/{id}` | Metadatos. |
| GET | `/api/docs/{id}/raw` | HTML original, para que el agente lo edite y lo resuba. |
| PATCH | `/api/docs/{id}` | JSON con `title`, `description`, `visibility`. |
| DELETE | `/api/docs/{id}` | Borrado definitivo, 204. |

Errores en `application/problem+json` (`type`, `title`, `status`, `detail`).

Ejemplo:

```bash
curl -sS -X POST "$ARTIFAX_URL/api/docs" \
  -H "Authorization: Bearer $ARTIFAX_TOKEN" \
  -F "file=@page.html" -F "title=Ventas Q3" -F "visibility=unlisted"

curl -sS -X PUT "$ARTIFAX_URL/api/docs/V1StGXR8Z5jd" \
  -H "Authorization: Bearer $ARTIFAX_TOKEN" \
  -H "Content-Type: text/html" --data-binary @page.html
```

### Validación de subidas

- No vacío y ≤ `ARTIFAX_MAX_UPLOAD_MB` (10 por defecto).
- UTF-8 válido, sin bytes NUL, parseable como HTML.
- El título, si no se envía, se toma del `<title>` del documento.

## Servir documentos

Cabeceras de `/d/{id}`:

```
Content-Security-Policy: sandbox allow-scripts allow-popups allow-forms allow-modals allow-downloads;
  default-src 'none'; script-src 'unsafe-inline' https:; style-src 'unsafe-inline' https:;
  img-src https: data: blob:; media-src https: data: blob:; font-src https: data:;
  connect-src https:; frame-ancestors 'none'
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
ETag: "<sha256>"
Cache-Control: public, max-age=60, must-revalidate   (public/unlisted)
Cache-Control: private, no-store                     (private)
```

- `sandbox` sin `allow-same-origin` hace que el documento corra en un origen opaco: su JS no puede leer la cookie de sesión ni actuar contra `/admin` o `/api` con la sesión del admin, aunque compartan host.
- Coste: dentro de los docs no funcionan `localStorage`, `sessionStorage` ni las cookies. La skill lo documenta.
- Recursos externos permitidos solo por `https:` (CDNs, fuentes, imágenes).

## Panel `/admin`

- **Docs**: tabla con búsqueda y filtro por visibilidad. Acciones: abrir, cambiar visibilidad, editar título y descripción, descargar el raw, borrar (con confirmación) y subida manual.
- **Tokens**: crear (secreto visible una vez), revocar y ver `last_used_at`.
- **Cabecera**: número de docs y espacio en disco.
- Cada doc muestra quién lo creó y quién lo actualizó por última vez (nombre del token o `admin`).

## Datos

```
docs      id(nanoid PK) · title · description · visibility
          · blob_sha256 · byte_size · source_filename
          · created_by · updated_by · created_at · updated_at
tokens    id · name · prefix · secret_hash · created_at · last_used_at · revoked_at
sessions  id · token_hash · subject · email · created_at · expires_at
```

- Blobs en `/data/blobs/ab/abcdef…` (direccionados por sha256, deduplicados). Se escriben primero a un temporal y luego se hace rename atómico.
- Al sobrescribir o borrar, el blob que ya no referencia ningún doc se elimina.

## Configuración (env)

| Variable | Default | Notas |
|---|---|---|
| `ARTIFAX_BASE_URL` | — | Obligatoria, p. ej. `https://artifax.example.com` |
| `ARTIFAX_LISTEN` | `:8080` | |
| `ARTIFAX_DATA_DIR` | `/data` | |
| `ARTIFAX_MAX_UPLOAD_MB` | `10` | |
| `ARTIFAX_BOOTSTRAP_TOKEN` | — | Opcional, debe empezar por `ax_` |
| `ARTIFAX_TRUST_PROXY` | `false` | Usar `X-Forwarded-For` para la IP del rate limit (solo detrás de proxy) |
| `AUTH_MODE` | — | `sso` \| `basic` |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | — | Modo `basic` |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | — | Modo `sso` |
| `ADMIN_GROUPS` | `admins` | Modo `sso`, separados por comas |
| `SESSION_TTL` | `168h` | |

## Estructura

```
cmd/artifax/main.go
internal/config      carga y validación de env
internal/store       SQLite, migraciones, queries
internal/blobs       almacenamiento en disco
internal/auth        sesiones, OIDC, basic, tokens, CSRF
internal/api         /api/*
internal/web         /, /d/{id}, /auth/*
internal/admin       /admin
internal/ui          templates + CSS embebidos
migrations/*.sql
skills/artifax/SKILL.md
Dockerfile · compose.example.yaml · README.md
```

## Fases

1. ✅ **Núcleo**: config, store y migraciones, blobs, API completa con el token de arranque, `/d/{id}` para docs públicos y unlisted, y tests con `httptest`.
2. ✅ **Auth**: sesiones, modos `basic` y `sso`, docs privados, logout. El flujo OIDC está probado contra un IdP falso.
3. ✅ **UI**: índice `/` y panel `/admin` (docs + tokens). Textos de la interfaz en inglés.
4. 🟡 **Entrega**: hechos `openapi.json`, skill, README y CI (tests + build). Pendientes: Dockerfile, `compose.example.yaml` y el workflow de imagen en GitHub Actions.

## Fuera de alcance v1

Versionado, assets separados, metadatos git, claves de idempotencia, subdominios, papelera, multiusuario, permisos por token.
