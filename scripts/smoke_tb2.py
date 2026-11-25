"""Prueba funcional del TB2 contra una instancia local de Docker Compose."""

import argparse
import json
import os
from pathlib import Path

from playwright.sync_api import expect, sync_playwright


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8080")
    parser.add_argument("--out", type=Path, default=Path("results/tb2"))
    parser.add_argument("--browser", help="Ruta opcional a un ejecutable de Chromium")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    base = args.base_url.rstrip("/")
    admin_pass = os.getenv("ADMIN_PASS", "admin123")
    analyst_pass = os.getenv("ANALISTA_PASS", "analista123")
    evidence = {}

    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path=args.browser)
        context = browser.new_context(base_url=base, viewport={"width": 1440, "height": 960})
        api = context.request

        assert api.get("/health").json()["estado"] == "listo"
        response = api.get("/api/modelo")
        assert response.status == 401
        evidence["sin_token_http"] = response.status
        analyst = api.post("/api/auth/login", data={"username": "analista", "password": analyst_pass}).json()
        authorization = {"Authorization": "Bearer " + analyst["token"]}
        for path, response in {
            "/api/entrenamientos": api.post("/api/entrenamientos", headers=authorization, data={"epocas": 1}),
            "/api/metricas": api.get("/api/metricas", headers=authorization),
        }.items():
            assert response.status == 403
            evidence[path + "_analista_http"] = response.status
        response = api.get("/api/modelo", params={"token": analyst["token"]})
        assert response.status == 401
        evidence["token_en_url_rest_http"] = response.status
        response = api.get("/api/modelo", headers={**authorization, "Origin": "https://example.invalid"})
        assert "access-control-allow-origin" not in response.headers
        evidence["cors_origen_ajeno"] = "sin autorización CORS"
        response = api.get("/ws", headers={
            "Origin": "https://example.invalid",
            "Connection": "Upgrade",
            "Upgrade": "websocket",
            "Sec-WebSocket-Version": "13",
            "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==",
            "Sec-WebSocket-Protocol": "epidemiologia, jwt." + analyst["token"],
        })
        assert response.status == 403
        evidence["ws_origen_ajeno_http"] = response.status

        page = context.new_page()
        page_errors = []
        page.on("pageerror", lambda error: page_errors.append(str(error)))

        def login(username, password):
            page.get_by_label("Usuario", exact=True).fill(username)
            page.get_by_label("Contraseña").fill(password)
            page.get_by_role("button", name="Ingresar").click()
            expect(page.get_by_role("heading", name="Panorama epidemiológico")).to_be_visible()
            expect(page.get_by_text("En vivo", exact=True)).to_be_visible(timeout=10000)

        page.goto("/")
        page.screenshot(path=args.out / "01_login.png")
        page.get_by_label("Usuario", exact=True).fill("analista")
        page.get_by_label("Contraseña").fill("incorrecta")
        page.get_by_role("button", name="Ingresar").click()
        expect(page.get_by_role("alert")).to_contain_text("usuario o contraseña incorrectos")

        login("analista", analyst_pass)
        expect(page.get_by_role("button", name="Clúster", exact=True)).to_have_count(0)
        if os.getenv("TB2_SYNTHETIC"):
            expect(page.get_by_text("Muestra sintética", exact=False)).to_be_visible()
        expect(page.get_by_role("table").get_by_role("row").nth(1)).to_be_visible()
        page.screenshot(path=args.out / "02_panorama.png")
        page.get_by_label("Departamento").select_option("LIMA")
        page.get_by_label("Ordenar por").select_option("tasa_por_1000")
        expect(page.get_by_role("table").get_by_role("row").nth(1)).to_contain_text("LIMA")
        page.get_by_role("button", name="Simulación").click()
        page.get_by_label("Edad en años").fill("111")
        assert not page.get_by_label("Edad en años").evaluate("(input) => input.validity.valid")
        page.get_by_label("Edad en años").fill("72")
        with page.expect_response(lambda r: r.url.endswith("/api/predicciones")) as first:
            page.get_by_role("button", name="Calcular riesgo").click()
        assert first.value.ok
        old = first.value.json()
        expect(page.get_by_text("Letalidad estimada", exact=True)).to_be_visible()
        page.screenshot(path=args.out / "03_simulacion.png")
        page.get_by_label("Edad en años").fill("25")
        with page.expect_response(lambda r: r.url.endswith("/api/predicciones")) as second:
            page.get_by_role("button", name="Calcular riesgo").click()
        young = second.value.json()
        assert second.value.ok and old["letalidad_estimada"] > young["letalidad_estimada"]
        evidence["simulaciones"] = [
            {key: row[key] for key in ("entrada", "letalidad_estimada", "nivel_riesgo", "latencia_ms", "modelo_version")}
            for row in (old, young)
        ]
        page.get_by_role("button", name="Entrenamiento", exact=True).click()
        expect(page.get_by_role("button", name="Entrenar modelo")).to_have_count(0)
        page.get_by_role("button", name="Cerrar sesión").click()
        expect(page.get_by_role("heading", name="Iniciar sesión")).to_be_visible()

        login("admin", admin_pass)
        page.get_by_role("button", name="Entrenamiento", exact=True).click()
        page.get_by_label("Nodos activos").select_option("4")
        page.get_by_label("Épocas").fill("2")
        with page.expect_response(lambda r: r.request.method == "POST" and r.url.endswith("/api/entrenamientos")) as job_response:
            page.get_by_role("button", name="Entrenar modelo").click()
        assert job_response.value.status == 202
        job_id = job_response.value.json()["id"]
        expect(page.get_by_role("status").filter(has_text="completado")).to_be_visible(timeout=180000)
        page.screenshot(path=args.out / "04_entrenamiento.png")
        page.get_by_role("button", name="Clúster", exact=True).click()
        expect(page.get_by_text("4 / 4", exact=True)).to_be_visible()
        expect(page.get_by_role("cell", name="completado", exact=True)).to_be_visible()
        page.screenshot(path=args.out / "05_cluster.png")
        page.get_by_role("button", name="Panorama", exact=True).click()
        page.set_viewport_size({"width": 390, "height": 844})
        expect(page.get_by_role("heading", name="Panorama epidemiológico")).to_be_visible()
        assert page.evaluate("document.documentElement.scrollWidth <= document.documentElement.clientWidth")
        page.screenshot(path=args.out / "06_movil.png")
        assert not page_errors, page_errors

        admin = api.post("/api/auth/login", data={"username": "admin", "password": admin_pass}).json()
        headers = {"Authorization": "Bearer " + admin["token"]}
        for name, path in (
            ("dataset", "/api/dataset"), ("entrenamiento", "/api/entrenamientos/" + job_id),
            ("impacto", "/api/impacto?top=15"), ("cluster", "/api/cluster"),
            ("metricas", "/api/metricas"),
        ):
            response = api.get(path, headers=headers)
            assert response.ok, response.status
            evidence[name] = response.json()
        assert evidence["entrenamiento"]["estado"] == "completado"
        assert len(evidence["entrenamiento"]["historial"]) == 2
        assert evidence["cluster"]["nodos_activos"] == 4
        assert evidence["dataset"]["positivos"] > 1000000
        evidence["spa"] = {"flujos": 6, "errores_js": page_errors, "estado_ws": "En vivo"}
        (args.out / "prueba_funcional.json").write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print("TB2 verificado: login, roles, filtros, simulación, entrenamiento, WebSocket, clúster y móvil.")
        browser.close()


if __name__ == "__main__":
    main()
