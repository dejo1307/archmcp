import gql from 'graphql-tag';

export const PAGE_STATS = gql`
  query PageStats {
    pageViews {
      total
    }
  }
`;

export const MISSING = gql`
  query Nowhere {
    somethingUnserved {
      id
    }
  }
`;
