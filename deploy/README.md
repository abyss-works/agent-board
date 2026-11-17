# 배포

운영 인스턴스는 k3s 클러스터에서 돈다. 매니페스트는 `k8s/prod/`에 있고, Argo CD가 `main`의 그 경로를 sync한다. 이미지 정의는 저장소 루트의 `Dockerfile`에, 자동 배포는 `.github/workflows/deploy.yml`에 있다. `deploy/compose.prod.yaml`은 전환기 폴백으로 남겨 둔다.

## 구성 요소

| 구성 요소 | 위치 | 역할 |
|---|---|---|
| 이미지 | `Dockerfile` | `web/`을 빌드해 Go 바이너리 `agb`와 함께 담는다. 이미지에는 `AGB_ADDR=0.0.0.0:3000`, `AGB_DATA_DIR=/data`, `AGB_WEB_DIR=/web`이 설정되어 있어, 서버가 `/v1` 밖의 경로를 웹 화면으로 서빙한다. |
| 클러스터 매니페스트 | `k8s/prod/` | `kustomization.yaml`이 Namespace `agb`, RoleBinding `traefik-secret-reader`, PVC `agb-data`, Deployment `agb`, Service `agb`, Certificate `agb-tls`, Ingress `agb`를 모은다. Deployment는 컨테이너 포트 3000으로 뜨고 노드를 `kubernetes.io/hostname: oci-node`로 고정한다. Service는 `app: agb` 파드를 80 포트로 받고, Ingress는 `<서비스 도메인>` 요청의 TLS를 끝내 Service로 넘긴다. RoleBinding은 네임스페이스 `agb`에서 ServiceAccount `traefik`(`traefik` 네임스페이스)에 ClusterRole `traefik-secret-reader`를 부여한다. |
| Argo CD Application | `k8s/argocd/application.yaml` | 저장소 `abyss-works/agent-board`의 `main` 브랜치, 경로 `k8s/prod`를 감시한다. `prune`과 `selfHeal`을 켠 자동 sync를 쓴다. sync 대상 밖에 두어 Application이 자기를 관리하는 순환을 피한다. |
| 운영 Compose(폴백) | `deploy/compose.prod.yaml` | 전환기 폴백이다. `ghcr.io/abyss-works/agent-board:<AGB_IMAGE_TAG>`를 빌드 없이 실행한다. 포트는 `AGB_BIND`(기본 `0.0.0.0`)의 `AGB_PORT`(기본 3000)에 열고, 데이터는 볼륨 `agb-data`에 둔다. |
| 워크플로 | `.github/workflows/deploy.yml` | `main` 푸시와 수동 실행에서 테스트, 이미지 빌드와 GHCR 업로드, 매니페스트 태그 커밋을 차례로 실행한다. 이미지 빌드는 arm64 러너에서, 나머지는 서버의 셀프 호스티드 러너에서 돈다. |

## 요청 경로

1. 클라이언트가 `https://<서비스 도메인>`으로 요청한다.
2. 클러스터의 인그레스(nginx)가 TLS를 끝내고 Service `agb`로 넘긴다.
3. Service가 `app: agb` 파드의 3000 포트로 보낸다.
4. 파드가 응답한다. `/v1` 아래는 API이고, 그 밖의 경로는 웹 화면이다.

## 배포 흐름

1. `main`에 푸시하면 테스트를 거쳐 `linux/arm64` 이미지를 `git-<커밋 7자>`와 `main` 태그로 GHCR에 올린다.
2. deploy 잡이 `k8s/prod/deployment.yaml`의 `image` 태그를 `git-<커밋 7자>`로 고쳐 `main`에 커밋하고 푸시한다. 커밋은 봇 신원(`github-actions[bot]`)으로 남고, `GITHUB_TOKEN`으로 푸시한다.
3. Argo CD가 그 커밋을 감지해 `main`의 `k8s/prod`를 sync하고 새 태그로 파드를 굴린다.
4. deploy 잡이 `https://<서비스 도메인>/v1/health`의 `version`이 새 태그인지 확인한다.

워크플로는 클러스터에 매니페스트를 직접 적용하지 않는다. `GITHUB_TOKEN`으로 만든 푸시는 워크플로를 다시 트리거하지 않으므로 배포가 반복되지 않는다.

## 클러스터에 미리 둘 것

- k3s 클러스터, `nginx` 인그레스 클래스, cert-manager와 ClusterIssuer `cloudflare-dns01`이 있어야 한다.
- Argo CD와 AppProject `agent-board`가 있어야 한다. Application `agb`가 이 프로젝트를 쓴다.
- RoleBinding `traefik-secret-reader`가 가리키는 ServiceAccount `traefik`(네임스페이스 `traefik`)와 ClusterRole `traefik-secret-reader`가 있어야 한다. 둘은 이 저장소의 매니페스트에 없다.
- Secret `agb-env`를 네임스페이스 `agb`에 만든다. 매니페스트는 이 Secret을 만들지 않는다. 키는 `AGB_TOKEN`, `AGB_ACTORS`, `AGB_BACKUP_BUCKET`, `AGB_BACKUP_ENDPOINT`, `AGB_BACKUP_ACCESS_KEY_ID`, `AGB_BACKUP_SECRET_ACCESS_KEY`, `AGB_EMBEDDING_API_KEY`, `AGB_EMBEDDING_URL`, `AGB_EMBEDDING_MODEL`, `AGB_EMBEDDING_QUERY_PREFIX`, `AGB_EMBEDDING_INTERVAL`, `AGB_EMBEDDING_QUERY_TIMEOUT`, `AGB_EMBEDDING_PROVIDER`다. 값은 저장소에 적지 않는다.
- Deployment의 `nodeSelector`가 `kubernetes.io/hostname: oci-node`이므로 그 호스트명을 가진 노드가 클러스터에 있어야 한다.
- Namespace `agb`는 Argo CD가 `k8s/prod/namespace.yaml`로 만든다(`CreateNamespace=true`). 미리 만들 필요가 없다.
- `<서비스 도메인>`(`k8s/prod/ingress.yaml`과 `certificate.yaml`에 적힌 호스트)의 DNS가 인그레스의 외부 주소를 가리켜야 한다.

## GitHub에 미리 해 둘 것

- Environment `production`을 만든다. 배포 브랜치 제한이나 승인자는 여기서 설정한다.
- `main`의 브랜치 보호 규칙이 `github-actions[bot]`의 푸시를 허용해야 한다. 허용하지 않으면 deploy 잡이 매니페스트 커밋을 푸시하지 못하고 오류로 끝난다.
- 첫 `image` 잡이 끝난 뒤 GHCR 패키지 `agent-board`의 Actions 접근 목록에 이 저장소가 있는지 확인한다. 없으면 읽기 이상의 권한으로 추가한다.
- Actions 설정에서 외부 협업자의 워크플로 실행에 승인을 요구한다.

## 처음 한 번 적용

Argo CD 프로젝트 `agent-board`를 만든 뒤 Application을 한 번 등록한다. 나머지 리소스는 Argo CD가 `main`의 `k8s/prod`를 sync하며 만든다.

전제: `main`에 deploy.yml이 남긴 `git-<커밋 7자>` 이미지 태그 커밋이 있어야 한다. 그 전에 Application을 만들면 첫 sync가 존재하지 않는 `git-PLACEHOLDER` 태그를 받아 파드가 `ImagePullBackOff`가 된다.

1. `main`에 푸시해 이미지 잡과 매니페스트 태그 커밋을 먼저 만든다. Application이 아직 없으므로 이 실행의 deploy 잡은 헬스 확인에서 실패한다.
2. `main`에 `git-<커밋 7자>` 태그 커밋이 있는지 확인하고 Application을 등록한다.
3. 워크플로를 수동 실행해 헬스 확인까지 통과하는지 본다. 매니페스트 태그가 이미 같으면 deploy 잡은 커밋 없이 헬스 확인만 한다.

```bash
git log -1 --format=%s origin/main
kubectl apply -f k8s/argocd/application.yaml
kubectl -n argocd get application agb
```

인증서가 `READY True`가 되면 Argo CD sync가 끝난 것이다.

```bash
kubectl -n agb get certificate agb-tls
```

## 수동 배포와 롤백

배포는 Argo CD에서 sync하거나 워크플로를 수동 실행(`workflow_dispatch`)해 시작한다. 이미지 태그를 직접 바꾸려면 `k8s/prod/deployment.yaml`의 `image` 태그를 고쳐 `main`에 푸시한다.

롤백은 이전 태그로 되돌려 커밋하고 푸시한다. Argo CD가 그 상태로 sync한다.

```bash
# k8s/prod/deployment.yaml
image: ghcr.io/abyss-works/agent-board:git-<이전 sha7>
```

데이터는 PVC `agb-data`에 있으므로 이미지를 바꿔도 남는다.

스키마 마이그레이션이 든 릴리즈는 이 방법으로 되돌릴 수 없다. 마이그레이션은 데이터베이스의 스키마 버전을 올리고, 이전 바이너리는 더 새로운 스키마를 만나면 `schema version N, expected M` 오류로 시작을 거부한다. 이런 릴리즈는 배포 직전에 운영 데이터베이스의 SQLite 스냅샷을 떠 둔다. 롤백은 파드를 멈추고, 스냅샷으로 `agb.db`를 되돌리고(`agb.db-wal`, `agb.db-shm`을 지운다. 절차는 아래 "백업에서 복원"의 3단계와 같다), 이전 태그로 띄우는 순서다.

### 전환기 Compose 폴백

k3s 이관이 끝나기 전에는 서버에서 `deploy/compose.prod.yaml`로 같은 이미지를 띄울 수 있다. 환경 파일을 만들고 소유자만 읽게 한다(`chmod 600`).

- `AGB_BIND`: 컨테이너 포트를 묶을 호스트 주소. 생략하면 `0.0.0.0`이다.
- `AGB_PORT`: 호스트 포트. 생략하면 3000이다.
- `AGB_TOKEN`, `AGB_ACTORS`: 위 Secret과 같은 키다.
- `AGB_BACKUP_*`, `AGB_EMBEDDING_*`: 위 Secret과 같은 키다. 각 키의 뜻은 저장소 루트 `README.md`의 환경 변수 표를 따른다.

```bash
export AGB_IMAGE_TAG=git-<sha7>
docker compose -f deploy/compose.prod.yaml --env-file <환경 파일 경로> pull
docker compose -f deploy/compose.prod.yaml --env-file <환경 파일 경로> up -d
```

## 백업에서 복원

1. 파드를 멈춘다: `kubectl -n agb scale deployment agb --replicas=0`
2. 버킷에서 가장 최근 객체(`agb/agb-<yyyymmdd>T<hhmm>Z.db.gz`)를 내려받아 gzip을 푼다.
3. PVC `agb-data`를 마운트한 임시 파드를 띄워 `/data/agb.db`를 그 파일로 바꾸고 `agb.db-wal`, `agb.db-shm`을 지운다. 파일 소유자는 컨테이너 사용자(65532)여야 한다.
4. 임시 파드를 지우고 파드를 다시 띄운 뒤 `/v1/health`를 확인한다: `kubectl -n agb scale deployment agb --replicas=1`

## 상태 확인

```bash
curl -fsS https://<서비스 도메인>/v1/health
```

응답의 `version`이 배포한 태그와 같으면 새 이미지가 떠 있는 것이다.
