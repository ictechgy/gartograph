# 릴리스 게시

버전·PR 검증이 끝난 정확한 커밋에 새 `vX.Y.Z` 태그를 만든다. 기존 Release workflow는
다섯 대상 빌드, 압축을 푼 macOS arm64 바이너리의 CLI 계약, 체크섬 생성을 마친 뒤에만 자산을 게시한다.
태그·버전 대조와 기존 권한·Homebrew 갱신 순서는 그대로다. 태그를 만들거나 옮기는 일은 게시 helper가 하지 않는다.

GitHub UI에서 먼저 릴리스를 만드는 경우에는 정확한 대상 커밋을 선택하고, 노트에 바이너리 게시가
아직 진행 중임을 적는다. 릴리스 페이지가 생겼다는 이유만으로 바이너리 배포 완료로 보지 않는다.
일반 공개 릴리스(`draft=false`, `prerelease=false`)에서만 누락 자산을 채우며 제목·본문·표시는 바꾸지 않는다.
이미 존재하는 예상 자산은 `uploaded` 상태·크기·SHA-256이 이번에 검증한 파일과 같아야 재사용한다.
다른 바이트·누락 digest·잘못된 커밋·API 오류는 게시를 중단한다. 다른 이름의 사용자 자산은 그대로 둔다.

helper는 여섯 예상 자산(다섯 압축 파일과 `checksums.txt`)을 모두 대조한 뒤에만 누락 이름을 올린다.
기존 자산에 `--clobber`·삭제를 적용하거나 태그·권한을 바꾸지 않는다. 실패하거나 결과가 불명확한 업로드는 재조회로
확인하며, 누락이 남으면 같은 검증 산출물로만 재시도한다. 다시 빌드한 압축 파일은 시간 정보 등으로
해시가 달라질 수 있으므로, 이름만 같다는 이유로 성공 처리하지 않는다. 태그 확인과 업로드는
원자적 트랜잭션이 아니다. 업로드 뒤 동시 태그 이동을 발견하면 실패하며, 이미 추가한 자산을
자동으로 삭제하지 않으므로 재시도 전에 해당 실행을 확인한다.

이미 공개된 immutable 릴리스는 누락 자산을 추가할 수 없다. helper는 이 경우 설정을 바꾸지 않고 실패한다.
릴리스가 없으면 기존 `gh release create --verify-tag` 경로를 유지한다. gh는 새 draft에 자산을
첨부한 뒤 공개하며, 업로드 실패 시 그 새 draft를 정리하는 동작도 그대로다. immutable 정책에서는
이처럼 자산 첨부를 마친 뒤 공개해야 한다.
릴리스 메타데이터에서 immutability 여부를 확인하지 못해도 게시를 중단한다.

최종 성공은 태그가 빌드 커밋을 가리키고 여섯 자산이 모두 검증된 바이트일 때다. 공개 다운로드·설치까지
확인한 뒤 준비 중인 노트를 갱신한다. Homebrew tap 자격 증명이 없으면 이전처럼 자동 갱신을 건너뛴다 —
이때 Release 실행은 성공으로 끝나지만 `Homebrew tap not updated` 경고와 실행 요약 표(대상별 SHA-256)를
남긴다. Release 성공을 tap 갱신 완료로 읽지 않는다.

## Homebrew tap 수동 갱신

`HOMEBREW_TAP_TOKEN`이 없을 때의 절차다. 새 자격 증명을 만들거나 권한을 넓히는 일은 이 절차에 없다.

1. 게시된 `checksums.txt`를 받아 공개 자산과 대조한다(`shasum -a 256 -c checksums.txt`). 실행 요약 표의 값과도 같아야 한다.
2. 이 저장소의 `Formula/gartograph.rb`에서 `@TAG@`·`@VERSION@`·`@SHA_DARWIN_ARM64@`·`@SHA_DARWIN_AMD64@`·
   `@SHA_LINUX_ARM64@`·`@SHA_LINUX_AMD64@` 자리표시자를 `release.yml`의 `sed`와 같은 값으로 채운다.
3. 채운 파일을 `ictechgy/homebrew-tap`의 `Formula/gartograph.rb`로 두고 `gartograph X.Y.Z` 커밋으로 반영한다(기존 관례).
4. `brew update && brew upgrade ictechgy/tap/gartograph`(새 설치는 `brew install`) 뒤 `gartograph --version`과
   `brew test ictechgy/tap/gartograph`로 설치본을 확인하고, 설치된 바이너리의 SHA-256이 자산과 같은지 본다.

오프라인 회귀 검사: `bash Scripts/test-publish-release-assets.sh`. 모든 GitHub 요청은 가짜 `gh`가 처리한다.
