import AccountPicker from './AccountPicker';
import { VISIBILITY_PUBLIC, VISIBILITY_RESTRICTED, VISIBILITY_PRIVATE } from '../../constants';

// 与后端 model.IsValidVisibility 一一对应（back/internal/model/anon.go）。
// 常量本体在 src/constants.js —— 组件不能从 api.js 取常量，那里被测试全量 mock 了。
export { VISIBILITY_PUBLIC, VISIBILITY_RESTRICTED, VISIBILITY_PRIVATE };

const OPTIONS = [
  { v: VISIBILITY_PUBLIC, icon: '🌐', label: '公开访问', hint: '任何人都能 pull，可广播' },
  { v: VISIBILITY_RESTRICTED, icon: '👥', label: '仅限权限', hint: '仅指定账号可见' },
  { v: VISIBILITY_PRIVATE, icon: '🔒', label: '仅自己', hint: '只有本节点运营者可见' },
];

/**
 * 广播权限三选项。restricted 会弹出账号选择器。
 * 坑：空 accessList 的 restricted 在后端会 400（否则这套合集谁也看不到，等于误设 private），
 * 所以选「仅限权限」后必须真的挑到人，UI 上直接把人数显示出来。
 */
export default function VisibilityPicker({ visibility, accessList = [], accounts = [], operator = '', onChange, onRequestAccounts }) {
  const current = visibility || VISIBILITY_PUBLIC;

  // 未绑定 regserver 账号时，「仅自己」在系统里无人能对上号（Owner 为空 → 谁都读不了）
  const privateDisabled = !operator;

  const pick = (v) => {
    if (v === VISIBILITY_PRIVATE && privateDisabled) return;
    if (v === VISIBILITY_RESTRICTED) {
      onRequestAccounts?.();
      return;
    }
    onChange?.(v, []);
  };

  return (
    <div className="space-y-1">
      <div className="flex gap-1">
        {OPTIONS.map(o => {
          const disabled = o.v === VISIBILITY_PRIVATE && privateDisabled;
          const active = current === o.v;
          return (
            <button key={o.v} onClick={() => pick(o.v)} disabled={disabled}
              title={disabled ? '未绑定 regserver 账号，无法使用「仅自己」' : o.hint}
              className={`flex-1 text-[11px] px-2 py-1 rounded transition-colors whitespace-nowrap ${
                active ? 'bg-brand-600 text-white font-medium' : 'bg-white/[0.06] text-gray-400 hover:text-white hover:bg-white/[0.08]'
              } ${disabled ? 'opacity-40 cursor-not-allowed' : ''}`}>
              {o.icon} {o.label}
            </button>
          );
        })}
      </div>

      {current === VISIBILITY_RESTRICTED && (
        <div className="flex items-center gap-2 px-1">
          <span className="text-[10px] text-gray-500 truncate flex-1">
            {accessList.length > 0
              ? `已选 ${accessList.length} 人：${accessList.slice(0, 3).join('、@')}${accessList.length > 3 ? '…' : ''}`
              : '尚未选择账号'}
          </span>
          <button onClick={() => onRequestAccounts?.()} className="text-[10px] px-2 py-0.5 rounded bg-white/[0.06] hover:bg-white/[0.12] text-gray-300">
            选择…
          </button>
        </div>
      )}

      {current === VISIBILITY_PRIVATE && (
        <p className="text-[10px] text-gray-500 px-1">仅 {operator || '本节点运营者'} 可见</p>
      )}
    </div>
  );
}

export { AccountPicker };
