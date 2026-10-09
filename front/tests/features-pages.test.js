// features-pages.test.js — verifies that all pages have been migrated to features/*
// and that pages/* re-export facades maintain backward compatibility.
//
// 发现背景：Issue #65 要求将剩余 8 个页面全部迁入 features/* 领域目录，
// 本用例验证 8 个功能域自持 pages 与 barrel 导出的完整性。

import { describe, it, expect } from 'vitest'

import * as btFeature from '../src/features/bt'
import * as ipfsFeature from '../src/features/ipfs'
import * as iwaraFeature from '../src/features/iwara'
import * as driveFeature from '../src/features/drive'
import * as transfersFeature from '../src/features/transfers'
import * as connectFeature from '../src/features/connect'
import * as nodeFeature from '../src/features/node'
import * as settingsFeature from '../src/features/settings'

import BTPage from '../src/features/bt/pages/BT'
import IPFSPage from '../src/features/ipfs/pages/IPFS'
import IwaraPage from '../src/features/iwara/pages/Iwara'
import DrivePage from '../src/features/drive/pages/Drive'
import TransfersPage from '../src/features/transfers/pages/Transfers'
import ConnectPage from '../src/features/connect/pages/Connect'
import NodeControlPage from '../src/features/node/pages/NodeControl'
import SettingsPage from '../src/features/settings/pages/Settings'

import BTPagesFacade from '../src/pages/BT'
import IPFSPagesFacade from '../src/pages/IPFS'
import IwaraPagesFacade, { fmtDuration, fmtCount, fmtDate, iwaraUrl, parseIds } from '../src/pages/Iwara'
import DrivePagesFacade from '../src/pages/Drive'
import TransfersPagesFacade from '../src/pages/Transfers'
import ConnectPagesFacade from '../src/pages/Connect'
import NodeControlPagesFacade from '../src/pages/NodeControl'
import SettingsPagesFacade from '../src/pages/Settings'

describe('Features pages migration and facade backward compatibility (Issue #65)', () => {
  it('feature pages export valid components and match barrel index exports', () => {
    expect(typeof BTPage).toBe('function')
    expect(btFeature.BT).toBe(BTPage)

    expect(typeof IPFSPage).toBe('function')
    expect(ipfsFeature.IPFS).toBe(IPFSPage)

    expect(typeof IwaraPage).toBe('function')
    expect(iwaraFeature.Iwara).toBe(IwaraPage)

    expect(typeof DrivePage).toBe('function')
    expect(driveFeature.Drive).toBe(DrivePage)

    expect(typeof TransfersPage).toBe('function')
    expect(transfersFeature.Transfers).toBe(TransfersPage)

    expect(typeof ConnectPage).toBe('function')
    expect(connectFeature.Connect).toBe(ConnectPage)

    expect(typeof NodeControlPage).toBe('function')
    expect(nodeFeature.NodeControl).toBe(NodeControlPage)

    expect(typeof SettingsPage).toBe('function')
    expect(settingsFeature.Settings).toBe(SettingsPage)
  })

  it('legacy pages/* facades preserve identical default and named exports', () => {
    expect(BTPagesFacade).toBe(BTPage)
    expect(IPFSPagesFacade).toBe(IPFSPage)
    expect(IwaraPagesFacade).toBe(IwaraPage)
    expect(DrivePagesFacade).toBe(DrivePage)
    expect(TransfersPagesFacade).toBe(TransfersPage)
    expect(ConnectPagesFacade).toBe(ConnectPage)
    expect(NodeControlPagesFacade).toBe(NodeControlPage)
    expect(SettingsPagesFacade).toBe(SettingsPage)

    expect(typeof fmtDuration).toBe('function')
    expect(typeof fmtCount).toBe('function')
    expect(typeof fmtDate).toBe('function')
    expect(typeof iwaraUrl).toBe('function')
    expect(typeof parseIds).toBe('function')
  })
})
