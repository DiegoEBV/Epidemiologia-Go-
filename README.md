# TF CC65 · Caso 1 · Riesgo epidemiológico COVID-19 con Regresión Logística Distribuida en Go

Entregable 1 (PC3): cargador concurrente + limpieza + EDA + entrenamiento paralelo
(Parameter Server con goroutines y canales) + evaluación + análisis de impacto.

## Datos

Descargar del Portal de Datos Abiertos y dejar en `data/`:

| Archivo | Dataset |
|---|---|
| `data/positivos_covid.csv` | [Casos positivos por COVID-19 – MINSA](https://www.datosabiertos.gob.pe/dataset/casos-positivos-por-covid-19-ministerio-de-salud-minsa) |
| `data/fallecidos_covid.csv` | [Fallecidos por COVID-19 – MINSA](https://www.datosabiertos.gob.pe/dataset/fallecidos-por-covid-19-ministerio-de-salud-minsa) |

El cargador ubica las columnas por **nombre** en el encabezado (no por posición) y detecta
el separador (`;` o `,`). Columnas usadas:

- Positivos: `DEPARTAMENTO`, `PROVINCIA`, `EDAD`, `SEXO`, `FECHA_RESULTADO`
- Fallecidos: `DEPARTAMENTO`, `PROVINCIA`, `EDAD_DECLARADA`, `SEXO`, `FECHA_FALLECIMIENTO`

Los identificadores (`id_persona`, `UUID`, `FECHA_NAC`, `UBIGEO` a nivel distrito) **no se cargan**.

Sin los CSV reales se puede probar con una muestra sintética del mismo esquema:

```bash
go run ./cmd/gensample -n 2500000 -out data
```

## Ejecutar

```bash
go test -race ./...                        # pruebas + detector de condiciones de carrera
go run ./cmd/pc3 -workers 4 -bench 1,2,4,8 # pipeline completo + benchmarks
python3 scripts/graficos.py results/resultados.json results/figs
```

Salidas: `results/resultados.json` (todas las métricas), `results/figs/*.png`.

## Estructura

```
cmd/pc3/          pipeline del entregable 1
cmd/gensample/    generador de muestra sintética (mismo esquema MINSA)
internal/data/    Parte A: esquema, parser/limpieza, cargador concurrente, EDA
internal/ml/      Parte B: regresión logística, SGD paralelo (parameter server), métricas, impacto
scripts/          gráficos del informe
```

## Git Flow

```bash
git checkout -b develop
git checkout -b feature/cargador-concurrente develop   # Parte A
git checkout -b feature/regresion-paralela develop     # Parte B
# PR feature/* -> develop ; release/pc3 -> main con tag v0.1-pc3
```
