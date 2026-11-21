#!/usr/bin/env python3
"""Genera las capturas del informe PC4 con Chrome headless.

- Salidas de terminal (archivos de texto de results/pc4) renderizadas como
  ventana de terminal.
- Swagger UI y reporte HTML de Newman servidos por la API / en disco.

    python3 scripts/capturas.py   (requiere el stack levantado y google-chrome)
"""
import html, os, subprocess, sys, tempfile

RAIZ = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RES = os.path.join(RAIZ, "results", "pc4")
OUT = os.path.join(RES, "capturas")
CHROME = os.environ.get("CHROME", "google-chrome")

SOLO = sys.argv[1:]  # p. ej. "mongo" para regenerar solo esas capturas

def chrome(url, png, ancho, alto, espera=4000):
    if SOLO and not any(x in os.path.basename(png) for x in SOLO):
        return
    subprocess.run([CHROME, "--headless=new", "--disable-gpu", "--hide-scrollbars", "--no-sandbox",
                    f"--window-size={ancho},{alto}", f"--virtual-time-budget={espera}",
                    f"--screenshot={png}", url], check=True, capture_output=True)
    print("  ✓", os.path.relpath(png, RAIZ))

def terminal(titulo, comando, texto, png, ancho=1180):
    lineas = texto.rstrip("\n").split("\n")
    alto = 92 + 18 * len(lineas) + 28
    cuerpo = html.escape(texto.rstrip("\n"))
    doc = f"""<!doctype html><meta charset="utf-8"><style>
    body{{margin:0;background:#e9ecef;font-family:'DejaVu Sans Mono',Consolas,monospace}}
    .win{{margin:14px;border-radius:9px;overflow:hidden;box-shadow:0 4px 14px rgba(0,0,0,.25);background:#1e1f29}}
    .bar{{background:#2d2f3b;color:#c9cbd6;font:13px sans-serif;padding:8px 12px;display:flex;align-items:center;gap:7px}}
    .d{{width:12px;height:12px;border-radius:50%;display:inline-block}}
    pre{{margin:0;padding:12px 16px 14px;color:#e6e6e6;font-size:13.2px;line-height:18px;white-space:pre}}
    .cmd{{color:#8be9fd}} .p{{color:#50fa7b}}</style>
    <div class="win"><div class="bar"><span class="d" style="background:#ff5f56"></span><span class="d" style="background:#ffbd2e"></span><span class="d" style="background:#27c93f"></span>&nbsp; {html.escape(titulo)}</div>
    <pre><span class="p">diego@tf-cc65</span>:<span class="cmd">~/Epidemiologia-Go-$ {html.escape(comando)}</span>
{cuerpo}</pre></div>"""
    with tempfile.NamedTemporaryFile("w", suffix=".html", delete=False, encoding="utf-8") as f:
        f.write(doc)
    chrome("file://" + f.name, png, ancho, alto, 500)
    os.unlink(f.name)

def leer(nombre, desde=None, hasta=None, filtro=None):
    with open(os.path.join(RES, nombre), encoding="utf-8") as f:
        ls = f.read().split("\n")
    if filtro:
        ls = [l for l in ls if filtro(l)]
    return "\n".join(ls[desde:hasta])

def main():
    os.makedirs(OUT, exist_ok=True)
    base = os.environ.get("API", "http://localhost:8080")
    c = lambda n: os.path.join(OUT, n)
    terminal("docker compose ps", "docker compose ps", leer("compose_ps.txt"), c("01_compose_ps.png"))
    terminal("docker network inspect", "docker network inspect epidemiologia-go_cluster epidemiologia-go_datos", leer("redes.txt"), c("02_redes.png"))
    terminal("API coordinadora · arranque", "docker compose logs api", leer("arranque_api.log"), c("03_arranque_api.png"))
    terminal("Nodo ML · node1", "docker compose logs node1", leer("node1.log"), c("04_node1.png"))
    terminal("go test -race", "go test -race -v ./internal/...", leer("tests_race.log"), c("05_tests_race.png"), 900)
    terminal("Benchmark del clúster", "python3 scripts/bench_cluster.py --nodos 1,2,3,4", leer("bench_cluster.txt"), c("06_bench_cluster.png"), 900)
    terminal("Prueba de carga", "go run ./cmd/loadtest -c 32 -d 20s", leer("loadtest.txt"), c("07_loadtest.png"))
    terminal("Prueba de carga durante un entrenamiento", "go run ./cmd/loadtest -c 32 -d 6s   # con un entrenamiento de 20 épocas en curso", leer("loadtest_entrenando.txt"), c("08_loadtest_entrenando.png"))
    cli = leer("newman_cli.txt")
    cortes = [cli.find("❏ 2."), cli.find("❏ 4.")]
    partes = [cli[:cortes[0]], cli[cortes[0]:cortes[1]], cli[cortes[1]:]]
    cmd = "newman run postman/TF-CC65-Caso1-PC4.postman_collection.json"
    for k, parte in enumerate(partes, 1):
        terminal(f"Postman · Newman ({k}/3)", cmd if k == 1 else cmd + "  (continuación)", parte.strip("\n"), c(f"{8 + k:02d}_newman_{k}.png"))
    tramos = [(0, 79, "colecciones, usuarios y modelos"), (79, 150, "entrenamientos"),
              (150, 226, "predicciones e impacto_provincias"), (226, None, "metricas_cluster y dataset_resumen")]
    for k, (d, h, t) in enumerate(tramos):
        terminal("MongoDB · " + t, "docker compose exec mongo mongosh epidemiologia", leer("mongo_evidencia.txt", d, h), c(f"{12 + k:02d}_mongo_{k + 1}.png"))
    terminal("MongoDB · índices y consultas", "docker compose exec mongo mongosh epidemiologia", leer("mongo_indices.txt"), c("16_mongo_indices.png"))
    terminal("Redis · caché", "docker compose exec redis sh", leer("redis_evidencia.txt"), c("17_redis.png"))
    chrome(base + "/swagger/", c("18_swagger.png"), 1400, 2350, 9000)
    chrome(base + "/swagger/#/Predicciones/post_api_predicciones", c("19_swagger_prediccion.png"), 1400, 2000, 9000)
    chrome("file://" + os.path.join(RES, "newman_reporte.html"), c("20_newman_html.png"), 1400, 1500, 6000)

if __name__ == "__main__":
    sys.exit(main())
