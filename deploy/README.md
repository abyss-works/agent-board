# 배포

운영 인스턴스를 서버 `ab`에 올리는 구성이다. 이 폴더에는 운영 Compose 파일과 클러스터 매니페스트가 있다. 이미지 정의는 저장소 루트의 `Dockerfile`에, 자동 배포는 `.github/workflows/deploy.yml`에 있다.

## 구성 요소

| 구성 요소 | 위치 | 역할 |
|---|---|---|
| 이미지 | `Dockerfile` | `web/`을 빌드해 Go 바이너리 `agb`와 함께 담는다. 이미지에는 `AGB_WEB_DIR=/web`이 설정되어 있어, 서버가 `/v1` 밖의 경로를 웹 화면으로 서빙한다. |
| 운영 Compose | `deploy/compose.prod.yaml` | `ghcr.io/abyss-works/agent-board:<AGB_IMAGE_TAG>`를 빌드 없이 실행한다. 포트는 `AGB_BIND` 주소에만 묶고, 데이터는 볼륨 `agb-data`에 둔다. |
| 클러스터 매니페스트 | `deploy/k8s/` | 인그레스가 `agb.abyssworks.dev` 요청을 ab의 컨테이너 포트로 보내게 한다. 셀렉터 없는 Service, Tailscale 주소를 가리키는 EndpointSlice, Certificate, Ingress로 이루어진다. |
| 워크플로 | `.github/workflows/deploy.yml` | `main` 푸시와 수동 실행에서 테스트, 이미지 빌드와 GHCR 업로드, ab 배포를 차례로 실행한다. 이미지 빌드는 arm64 러너에서, 배포는 ab의 셀프 호스티드 러너에서 돈다. |

## 요청 경로

1. 클라이언트가 `https://agb.abyssworks.dev`로 요청한다.
2. ab의 k3s 인그레스(nginx)가 TLS를 끝내고 Service `agb-host`로 넘긴다.
3. Service는 EndpointSlice에 적힌 `100.104.90.109:8420`으로 보낸다.
4. 이 주소에 묶인 컨테이너가 응답한다. `/v1` 아래는 API이고, 그 밖의 경로는 웹 화면이다.

EndpointSlice의 주소와 포트는 환경 파일의 `AGB_BIND`, `AGB_PORT`와 같아야 한다.

## ab에 미리 둘 것

### 환경 파일

`/home/ubuntu/agb/.env`를 만들고 소유자만 읽게 한다(`chmod 600`). 배포 잡과 수동 배포가 `--env-file`로 이 파일을 읽는다. 키는 다음과 같다. 값은 저장소에 적지 않는다.

- `AGB_BIND`: 컨테이너 포트를 묶을 호스트 주소. ab의 Tailscale 주소를 쓴다.
- `AGB_PORT`: 호스트 포트. 생략하면 8420이다. 워크플로의 헬스 확인과 EndpointSlice가 8420을 쓰므로, 바꾸면 둘도 함께 바꾼다.
- `AGB_TOKEN`: 공용 토큰. 32자 이상이어야 한다.
- `AGB_ACTORS`: 행위자 목록. `id:kind`를 쉼표로 잇는다. 예: `owner:user,claude:agent`
- `AGB_BACKUP_BUCKET`, `AGB_BACKUP_ENDPOINT`, `AGB_BACKUP_ACCESS_KEY_ID`, `AGB_BACKUP_SECRET_ACCESS_KEY`: 백업 설정이며 모두 생략할 수 있다. 버킷을 지정하면 나머지 셋도 필요하다. 이 저장소의 `agb serve`는 객체 저장소 어댑터 없이 서버를 만들므로, 버킷을 지정하면 서버가 시작하지 않는다.

### 러너

- 조직 셀프 호스티드 러너를 ab에 두고 라벨 `self-hosted`와 `ab`를 붙인다.
- 러너 사용자는 `docker`와 `docker compose`를 실행할 수 있어야 하고, 환경 파일을 읽을 수 있어야 한다.
- 러너가 속한 그룹이 저장소 `abyss-works/agent-board`(공개)의 사용을 허용해야 한다.

### 클러스터

- cert-manager와 ClusterIssuer `cloudflare-dns01`, `nginx` 인그레스 클래스가 있어야 한다.
- `agb.abyssworks.dev`의 DNS가 인그레스의 외부 주소를 가리켜야 한다.

## GitHub에 미리 해 둘 것

- Environment `production`을 만든다. 배포 브랜치 제한이나 승인자는 여기서 설정한다.
- 첫 `image` 잡이 끝난 뒤 GHCR 패키지 `agent-board`의 Actions 접근 목록에 이 저장소가 있는지 확인한다. 없으면 읽기 이상의 권한으로 추가한다.
- Actions 설정에서 외부 협업자의 워크플로 실행에 승인을 요구한다.

## 처음 한 번 적용

클러스터 매니페스트는 워크플로가 적용하지 않는다. 처음 한 번 직접 적용한다.

```bash
kubectl apply -k deploy/k8s
kubectl -n agb get certificate agb-tls
```

인증서가 `READY True`가 되면 `main`에 푸시하거나 워크플로를 수동으로 실행해 첫 배포를 한다.

## 수동 배포와 롤백

ab에서 저장소를 체크아웃한 폴더에서 실행한다. 이미지 태그는 `git-<커밋 앞 7자리>`다. 올라간 태그는 GHCR 패키지 페이지에서 확인한다. 이미지가 ab에 없고 패키지가 비공개이면 먼저 `docker login ghcr.io`로 로그인한다.

```bash
export AGB_IMAGE_TAG=git-<sha7>
docker compose -f deploy/compose.prod.yaml --env-file /home/ubuntu/agb/.env pull
docker compose -f deploy/compose.prod.yaml --env-file /home/ubuntu/agb/.env up -d
```

롤백은 이전 태그로 `up -d`를 다시 실행한다.

```bash
AGB_IMAGE_TAG=git-<이전 sha7> docker compose -f deploy/compose.prod.yaml --env-file /home/ubuntu/agb/.env up -d
```

데이터는 볼륨 `agb_agb-data`에 있으므로 이미지를 바꿔도 남는다.

## 상태 확인

```bash
curl -fsS http://100.104.90.109:8420/v1/health
curl -fsS https://agb.abyssworks.dev/v1/health
```

응답의 `version`이 배포한 태그와 같으면 새 이미지가 떠 있는 것이다.
