import React from 'react';

interface PaginationProps {
  currentPage: number;
  totalPages: number;
  onPageChange: (page: number) => void;
  loading?: boolean;
}

const Pagination: React.FC<PaginationProps> = ({
  currentPage,
  totalPages,
  onPageChange,
  loading = false
}) => {
  if (totalPages <= 1) return null;

  const getPageNumbers = () => {
    const pages = [];
    const maxVisiblePages = 5;
    
    let startPage = Math.max(1, currentPage - Math.floor(maxVisiblePages / 2));
    const endPage = Math.min(totalPages, startPage + maxVisiblePages - 1);
    
    // Adjust start page if we're near the end
    if (endPage - startPage + 1 < maxVisiblePages) {
      startPage = Math.max(1, endPage - maxVisiblePages + 1);
    }
    
    // Add first page and ellipsis if needed
    if (startPage > 1) {
      pages.push(1);
      if (startPage > 2) {
        pages.push('...');
      }
    }
    
    // Add visible pages
    for (let i = startPage; i <= endPage; i++) {
      pages.push(i);
    }
    
    // Add ellipsis and last page if needed
    if (endPage < totalPages) {
      if (endPage < totalPages - 1) {
        pages.push('...');
      }
      pages.push(totalPages);
    }
    
    return pages;
  };

  const handlePageClick = (page: number | string) => {
    if (typeof page === 'number' && page !== currentPage && !loading) {
      onPageChange(page);
    }
  };

  return (
    <div className="flex justify-center items-center space-x-2 mt-8">
      <div className="join">
        {/* Previous Button */}
        <button
          className="join-item btn btn-sm"
          onClick={() => handlePageClick(currentPage - 1)}
          disabled={currentPage === 1 || loading}
          aria-label="Previous page"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            className="h-4 w-4"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth="2"
              d="M15 19l-7-7 7-7"
            />
          </svg>
        </button>

        {/* Page Numbers */}
        {getPageNumbers().map((page, index) => (
          <button
            key={index}
            className={`join-item btn btn-sm ${
              page === currentPage ? 'btn-active' : ''
            } ${page === '...' ? 'btn-disabled' : ''}`}
            onClick={() => handlePageClick(page)}
            disabled={loading || page === '...'}
          >
            {loading && page === currentPage ? (
              <span className="loading loading-spinner loading-xs"></span>
            ) : (
              page
            )}
          </button>
        ))}

        {/* Next Button */}
        <button
          className="join-item btn btn-sm"
          onClick={() => handlePageClick(currentPage + 1)}
          disabled={currentPage === totalPages || loading}
          aria-label="Next page"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            className="h-4 w-4"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth="2"
              d="M9 5l7 7-7 7"
            />
          </svg>
        </button>
      </div>
      
      {/* Page Info */}
      <div className="text-sm text-base-content/60 ml-4">
        Page {currentPage} of {totalPages}
      </div>
    </div>
  );
};

export default Pagination;