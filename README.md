# TF CC65 · Caso 1 · Riesgo epidemiológico COVID-19 con Regresión Logística Distribuida en Go

- **Entregable 1 (PC3):** cargador concurrente + limpieza + EDA + entrenamiento paralelo
  (Parameter Server con goroutines y canales) + evaluación + análisis de impacto.
- **Entregable 2 (PC4):** clúster de 4 nodos ML por TCP, API coordinadora REST/WebSocket con
  Swagger y JWT, MongoDB (persistente) y Redis (caché), todo con Docker Compose.

## Dataset y Datos

Descargar del Portal de Datos Abiertos y dejar en `data/`:

| Archivo | Dataset |
|---|---|
| `data/positivos_covid.csv` | [Casos positivos por COVID-19 – MINSA](https://www.datosabiertos.gob.pe/dataset/casos-positivos-por-covid-19-ministerio-de-salud-minsa) |
| `data/fallecidos_covid.csv` | [Fallecidos por COVID-19 – MINSA](https://www.datosabiertos.gob.pe/dataset/fallecidos-por-covid-19-ministerio-de-salud-minsa) |

El cargador ubica las columnas por **nombre** en el encabezado y detecta el separador (`;` o `,`).
Los identificadores (`id_persona`, `UUID`, `FECHA_NAC`, `UBIGEO`) **no se cargan**.

Sin los CSV reales: `go run ./cmd/gensample -n 2500000 -out data`.

## PC4: levantar el sistema

```bash
docker compose up --build -d          # node1..node4 + api + mongo + redis
docker compose logs -f api            # esperar "estado del coordinador: listo"
open http://localhost:8080/swagger/   # documentación interactiva
```

Usuarios de prueba: `admin / admin123` (puede entrenar) y `analista / analista123`.
Las credenciales y el secreto JWT se cambian con `ADMIN_PASS`, `ANALISTA_PASS` y `JWT_SECRET`.

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
| GET | `/api/metricas` | Latencia p50/p95/p99 por ruta |
| GET | `/ws?token=` | Eventos en tiempo real |

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
