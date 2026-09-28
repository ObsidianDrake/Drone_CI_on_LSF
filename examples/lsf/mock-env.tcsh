#!/bin/tcsh
# 從 repository 根目錄執行：source examples/lsf/mock-env.tcsh
# 只在本機模擬環境使用；正式叢集請使用公司的 LSF client。
if ( -x "${cwd}/tools/lsf-mock/bin/bsub" ) then
    if ( "$path[1]" != "${cwd}/tools/lsf-mock/bin" ) then
        setenv PATH "${cwd}/tools/lsf-mock/bin:${PATH}"
    endif
    setenv LSF_MOCK_STATE_DIR /tmp/drone-lsf-mock-$USER
    rehash
else
    echo "請先切換到 Drone repository 根目錄，再 source 此腳本。"
endif
