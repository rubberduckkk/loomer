"""Local operator console. Access the staging port through an SSH tunnel."""
import os
from pathlib import Path

import httpx
import uvicorn
from starlette.applications import Starlette
from starlette.middleware.trustedhost import TrustedHostMiddleware
from starlette.responses import FileResponse, JSONResponse, Response
from starlette.routing import Route


def page(request):
    return FileResponse(Path(__file__).with_name("admin.html"), headers={"X-Frame-Options": "DENY"})


async def proxy(request):
    # A custom header and no CORS prevent cross-origin browser requests.
    if request.headers.get("x-looomer-admin") != "1":
        return JSONResponse({"error": "admin header required"}, 403)
    headers = {"Authorization": "Bearer " + os.environ["LOOOMER_API_TOKEN"]}
    if request.method == "POST":
        headers["Content-Type"] = "application/json"
        headers["Idempotency-Key"] = request.headers["idempotency-key"]
    async with httpx.AsyncClient(base_url=os.environ["LOOOMER_API_URL"], trust_env=False) as client:
        result = await client.request(request.method, request.url.path, params=request.query_params.multi_items(),
                                      content=await request.body(), headers=headers)
    return Response(result.content, status_code=result.status_code, media_type="application/json",
                    headers={"Cache-Control": "no-store"})


routes = [Route("/", page), Route("/v1/runs", proxy, methods=["POST"])]
for path in ("/v1/jobs", "/v1/apps", "/v1/events/count", "/v1/jobs/{id}", "/v1/jobs/{id}/input",
             "/v1/apps/{app}/records/{collection}/{id}"):
    routes.append(Route(path, proxy))
app = Starlette(routes=routes)
app.add_middleware(TrustedHostMiddleware, allowed_hosts=["localhost", "127.0.0.1"])

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8081)
