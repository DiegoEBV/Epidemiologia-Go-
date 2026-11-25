# TF CC65 · Caso 1 · Riesgo epidemiológico COVID-19 con Regresión Logística Distribuida en Go

- **Entregable 1 (PC3):** cargador concurrente + limpieza + EDA + entrenamiento paralelo
  (Parameter Server con goroutines y canales) + evaluación + análisis de impacto.
- **Entregable 2 (PC4):** clúster de 4 nodos ML por TCP, API coordinadora REST/WebSocket con
  Swagger y JWT, MongoDB (persistente) y Redis (caché), todo con Docker Compose.
- **Entregable 3 (TB2):** SPA React con panorama provincial, simulador, entrenamiento
  en vivo y administración del clúster. La API de Go sirve los recursos compilados.

## Dataset y Datos

Descargar del Portal de Datos Abiertos y dejar en `data/`:

| Archivo | Dataset |
|---|---|
| `data/positivos_covid.csv` | [Casos positivos por COVID-19 – MINSA](https://www.datosabiertos.gob.pe/dataset/casos-positivos-por-covid-19-ministerio-de-salud-minsa) |
| `data/fallecidos_covid.csv` | [Fallecidos por COVID-19 – MINSA](https://www.datosabiertos.gob.pe/dataset/fallecidos-por-covid-19-ministerio-de-salud-minsa) |

El cargador ubica las columnas por **nombre** en el encabezado y detecta el separador (`;` o `,`).
Los identificadores (`id_persona`, `UUID`, `FECHA_NAC`, `UBIGEO`) **no se cargan**.

Sin los CSV reales, genera una muestra determinista:

```bash
go run ./cmd/gensample -n 1100000 -out data -seed 7
```

Configura `DATA_SOURCE` como `Muestra sintética reproducible` para esa muestra.
Con datos oficiales usa `MINSA (datos históricos)`. `DATA_PATH` permite montar
otra carpeta de CSV. Define estas variables en el entorno o en un archivo `.env` local.

## TB2: levantar el sistema

```bash
docker compose up --build -d          # node1..node4 + API/SPA + MongoDB + Redis
docker compose logs -f api            # esperar "estado del coordinador: listo"
```

Abre [la interfaz](http://localhost:8080) o [Swagger](http://localhost:8080/swagger/).
Para usar otro puerto local, configura `HTTP_PORT` antes del despliegue.

Usuarios de prueba: `admin / admin123` (puede entrenar) y `analista / analista123`.
Las credenciales y el secreto JWT se cambian con `ADMIN_PASS`, `ANALISTA_PASS` y `JWT_SECRET`.
Las cuentas se crean al inicializar MongoDB. Usa un volumen nuevo si necesitas otras credenciales.
El puerto se vincula a loopback para la demostración local.

```
              :8080
cliente ───────────► api (coordinador)
                      │  │
   red "cluster" TCP  │  │  red "datos"
   ┌──────┬──────┬────┘  └────┬───────┐
 node1  node2  node3  node4  mongo   redis
```

- Topología en estrella: cada nodo tiene una conexión TCP persistente con la API (mensajes `encoding/gob`).
- `cluster` y `datos` son redes `internal`: los nodos no ven las bases ni Internet; solo se publica el 8080.
- Cada nodo usa 1 CPU (`cpus: 1`, `GOMAXPROCS=1`) para simular una máquina independiente.

### Endpoints principales

| Método | Ruta | Descripción |
|---|---|---|
| POST | `/api/auth/login` | JWT (HS256) |
| POST | `/api/predicciones` | Riesgo de un caso (caché Redis, auditoría en MongoDB) |
| GET | `/api/impacto?top=15` | Provincias con más casos graves esperados (calculado en el clúster) |
| POST | `/api/entrenamientos` | Entrenamiento distribuido (admin) |
| GET | `/api/cluster` | Estado de los nodos (latido, RTT, CPU) |
| GET | `/api/metricas` | Latencia p50/p95/p99 por ruta (admin) |
| GET | `/ws` | Eventos en vivo. La SPA negocia `epidemiologia` y envía el JWT en un subprotocolo separado |

La SPA usa rutas relativas del mismo origen y guarda el JWT en memoria durante la sesión.
`/api/entrenamientos` (POST) y `/api/metricas` requieren admin. WebSocket valida el origen
en el navegador y cierra la sesión al expirar el token. Para publicar fuera de loopback,
configura TLS en un proxy, credenciales propias y un secreto JWT distinto del de demostración.

El ranking usa la suma de estimaciones de letalidad por provincia como aproximación exploratoria
a la carga de casos graves. El panel permite comparar volumen y tasa por mil positivos registrados.
Los resultados corresponden a la ventana histórica disponible en los CSV.

### Pruebas y evidencias

```bash
go test -race ./...                                              # 16 pruebas
npx newman run postman/TF-CC65-Caso1-PC4.postman_collection.json # Postman (22 peticiones, 87 aserciones)
go run ./cmd/loadtest -c 32 -d 20s -out results/pc4/latencia.json
python3 scripts/bench_cluster.py --nodos 1,2,3,4                  # escalamiento del clúster
python3 scripts/capturas.py                                      # capturas del informe (Chrome headless)
docker compose exec mongo mongosh epidemiologia                  # inspeccionar MongoDB
docker compose exec redis redis-cli                              # inspeccionar Redis
```

Resultados: `results/pc4/` (JSON, logs y capturas) y `results/figs/pc4_*.png`.
Informe: `cd informe && npm i docx@9 && node build_pc4.js`.

Para repetir la prueba funcional del TB2 con Docker listo y cuatro nodos activos:

```bash
python -m pip install playwright
python -m playwright install chromium
python scripts/smoke_tb2.py --base-url http://127.0.0.1:8080 --out results/tb2
go run ./cmd/loadtest -url http://127.0.0.1:8080 -c 32 -d 20s -out results/tb2/latencia.json
```

El smoke test autentica ambos roles, verifica controles de acceso, ejecuta dos simulaciones
y solicita dos épocas al clúster. Genera capturas y evidencia JSON en `results/tb2/`.

## PC3: pipeline local

```bash
go run ./cmd/pc3 -workers 4 -bench 1,2,4,8 # pipeline completo + benchmarks
python3 scripts/graficos.py results/resultados.json results/figs
```

## Estructura

```
cmd/pc3/            pipeline del entregable 1
cmd/node/           nodo ML (servidor TCP)
cmd/api/            API REST/WebSocket + coordinador del clúster
cmd/loadtest/       prueba de carga
cmd/gensample/      generador de muestra sintética (mismo esquema MINSA)
internal/data/      Parte A: esquema, parser/limpieza, cargador concurrente, EDA
internal/ml/        Parte B: regresión logística, ShardLocal, Coordinar (Parameter Server), métricas
internal/cluster/   protocolo gob, nodo, cliente TCP, coordinador (entrenamiento e impacto distribuidos)
internal/store/     entidades MongoDB y caché Redis
internal/api/       handlers, JWT/PBKDF2, latencias, WebSocket, Swagger embebido
postman/            colección de pruebas
scripts/            gráficos, benchmark del clúster, capturas, diagramas
```

## Git Flow

```bash
git checkout -b feature/cluster-tcp develop
git checkout -b feature/api-coordinadora develop
git checkout -b feature/bases-datos develop
# PR feature/* -> develop ; release/pc4 -> main con tag v0.2-pc4
```
