import { Link } from 'react-router-dom';

export default function EmptyState() {
  return (
    <div className="flex-1 flex flex-col items-center justify-center text-gray-600 text-sm gap-4">
      <p>输入合集 Hash 查看内容</p>
      <div className="flex gap-3">
        <Link to="/create" className="text-blue-400 hover:underline">创建合集</Link>
        <Link to="/" className="text-blue-400 hover:underline">浏览合集</Link>
      </div>
    </div>
  );
}
